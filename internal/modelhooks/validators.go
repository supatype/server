package modelhooks

import (
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/utilities"
)

// Per-field validators: a rule the database cannot express, run before the write.
//
// They share the `beforeChange` interception rather than getting a path of their own. A second
// dispatch point would double the places a write can be refused, and the two would drift on the
// questions that matter: what happens when the function is unreachable, how a rejection reaches the
// caller, whether `previous()` is available. One path, one answer.
//
// What distinguishes a validator from a `beforeChange` hook is scope and blame. A hook receives the
// whole row and speaks for the write; a validator receives **one field's value** and its refusal is
// attributed to that field, so Studio can put the message on the input rather than in a banner.
// That attribution is the entire reason this is not just advice to write a hook.

// ValidatorsFunc supplies the per-table field validators for a request, keyed by table.
type ValidatorsFunc func(*http.Request) map[string]TableValidatorsView

// TableValidatorsView is one table's validators, keyed by **column** name.
type TableValidatorsView map[string]HookConfigEntry

// validatorPayload is what a field validator receives.
//
// Deliberately not the hook payload with an extra key. A validator is handed one value and asked one
// question, and a payload carrying the whole row would invite it to answer a different one, at which
// point the field attribution its refusal gets would be a lie.
type validatorPayload struct {
	Table     string          `json:"table"`
	Operation Operation       `json:"operation"`
	Field     string          `json:"field"`
	Value     json.RawMessage `json:"value"`
	RequestID string          `json:"requestId"`
	User      *Claims         `json:"user"`
}

// fieldValues pulls one column's value out of each row in the request body.
//
// An insert may carry several rows and a patch carries one object; both are handled, because a
// validator that ran on the first row of a batch and not the rest would be worse than one that did
// not run at all. A row that does not mention the column yields nothing: an absent field is not a
// value to validate, and the column's own bounds and NOT NULL already speak to absence.
func fieldValues(body []byte, field string) []json.RawMessage {
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		var single map[string]json.RawMessage
		if err := json.Unmarshal(body, &single); err != nil {
			return nil
		}
		rows = []map[string]json.RawMessage{single}
	}

	out := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		if value, ok := row[field]; ok {
			out = append(out, value)
		}
	}
	return out
}

// rejectionBody is what a caller receives when a validator says no.
//
// The field is named separately from the message so a client does not have to parse prose to know
// where to put it. `422` rather than `400`: the request was understood and the value was refused.
type rejectionBody struct {
	Error   string `json:"error"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// runValidators calls every declared field validator for this write.
//
// Returns false when the write must not proceed, having already written the response.
func runValidators(
	w http.ResponseWriter,
	req *http.Request,
	target Target,
	body []byte,
	opts Options,
	log *logrus.Entry,
	depth int,
) bool {
	for _, field := range target.ValidatorFields {
		cfg := target.Validators[field]
		view := HookConfigView{TimeoutMs: cfg.TimeoutMs, OnUnavailable: cfg.OnUnavailable}
		fieldLog := log.WithFields(logrus.Fields{
			"event":    validatorEvent,
			"function": cfg.Function,
			"field":    field,
		})

		values := fieldValues(body, field)
		if len(values) == 0 {
			continue
		}

		url, err := opts.ResolveURL(req, cfg.Function)
		if err != nil {
			_, ok := unavailable(w, view, validatorEvent, "resolving the function URL: "+err.Error(), fieldLog)
			if !ok {
				return false
			}
			continue
		}

		for _, value := range values {
			encoded, err := json.Marshal(validatorPayload{
				Table:     target.Table,
				Operation: target.Operation,
				Field:     field,
				Value:     value,
				RequestID: opts.RequestID(req),
				User:      opts.Claims(req),
			})
			if err != nil {
				_, ok := unavailable(w, view, validatorEvent, "encoding the payload: "+err.Error(), fieldLog)
				if !ok {
					return false
				}
				break
			}

			outcome := opts.Dispatcher.Call(req.Context(), url, validatorEvent, view, encoded, depth+1)
			switch outcome.Kind {
			case OutcomeReject:
				fieldLog.WithField("status", outcome.Status).Info("field validator rejected the write")
				writeFieldRejection(w, field, outcome)
				return false
			case OutcomeUnavailable:
				if _, ok := unavailable(w, view, validatorEvent, outcome.Reason, fieldLog); !ok {
					return false
				}
			}
		}
	}
	return true
}

// validatorEvent is the event name a validator sees, and the key `RejectsWhenUnavailable` reads.
//
// Listed in that policy's default alongside the other `before` events, not merely prefixed with the
// word: the policy matches exact names, so a new event that is not added there defaults to letting
// writes through when it cannot be reached. Silently accepting a value nobody checked is the failure
// this feature exists to prevent.
const validatorEvent = EventBeforeValidate

// writeFieldRejection sends the validator's message with the field attached.
//
// The validator's own body is preferred when it already names a field, so a handler that wants to
// say something more specific is not overwritten by this.
func writeFieldRejection(w http.ResponseWriter, field string, outcome Outcome) {
	status := outcome.Status
	if status == 0 {
		status = http.StatusUnprocessableEntity
	}

	message := ""
	if len(outcome.Body) > 0 {
		var existing rejectionBody
		if err := json.Unmarshal(outcome.Body, &existing); err == nil {
			if existing.Field != "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write(outcome.Body)
				return
			}
			message = utilities.FirstNonEmpty(existing.Message, existing.Error)
		}
	}
	if message == "" {
		message = "This value was rejected."
	}

	encoded, err := json.Marshal(rejectionBody{Error: message, Field: field, Message: message})
	if err != nil {
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}
