package modelhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/utilities"
)

// MaxBodyBytes caps what will be buffered to show a hook.
//
// A before hook cannot be called without the body, so an oversized body is refused rather than
// waved through: skipping the hook would mean a write the schema said to validate arriving
// unvalidated, with nothing in the response to say so.
const MaxBodyBytes int64 = 1 << 20 // 1 MiB

// afterHookTimeout bounds the whole after-phase, separately from the per-hook timeout, so a slow
// after hook cannot hold a connection open indefinitely once the client already has its answer.
const afterHookTimeout = 30 * time.Second

// UpstreamResolver returns the URL to POST a named function to.
// Wired to the server's existing functions resolution rather than duplicating it.
type UpstreamResolver func(req *http.Request, function string) (string, error)

// Claims is the caller identity a hook payload carries.
type Claims struct {
	Sub   string `json:"sub"`
	Role  string `json:"role"`
	Email string `json:"email,omitempty"`
}

// ClaimsFunc reads the verified caller identity from a request, or nil for an anonymous one.
type ClaimsFunc func(*http.Request) *Claims

// HooksFunc returns the current hook map — read per request, since the manifest is hot-reloaded.
type HooksFunc func(*http.Request) map[string]TableHooksView

// TableHooksView is one table's hooks, decoupled from the manifest type so this package does not
// depend on how they were delivered.
type TableHooksView map[string]HookConfigEntry

// HookConfigEntry is one hook's configuration.
type HookConfigEntry struct {
	Function      string
	TimeoutMs     int
	OnUnavailable string
}

// Options configures Middleware.
type Options struct {
	Dispatcher *Dispatcher
	Hooks      HooksFunc
	// Validators supplies per-field rules. Optional: a project with none is the common case and
	// must stay free, which is why this is a separate lookup rather than a field on every hook view.
	Validators   ValidatorsFunc
	ResolveURL   UpstreamResolver
	Claims       ClaimsFunc
	RequestID    func(*http.Request) string
	MaxBodyBytes int64
	// Callback mints the `previous()` path. Optional: without it the context simply has no
	// `previous`, which the generated types already model as absent rather than as a broken call.
	Callback *Callback
}

type payload struct {
	Table     string          `json:"table"`
	Operation Operation       `json:"operation"`
	RequestID string          `json:"requestId"`
	User      *Claims         `json:"user"`
	Rows      json.RawMessage `json:"rows,omitempty"`
	Patch     json.RawMessage `json:"patch,omitempty"`
	Filter    string          `json:"filter,omitempty"`
	// PreviousPath is a path, not a URL: the worker already knows how to reach the stack and the
	// server does not know its own in-network address. The generated adapter joins them.
	PreviousPath string `json:"previousPath,omitempty"`
}

// Middleware runs a table's declared hooks around a write.
//
// Mounted inside the response cache, so a cached read never reaches it. A request with no hook work
// is handed straight through with its body untouched — that is the overwhelmingly common case and it
// must stay free.
func Middleware(opts Options) func(http.Handler) http.Handler {
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = MaxBodyBytes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			target := classifyFor(req, opts.Hooks, opts.Validators)
			if !target.HasWork() {
				next.ServeHTTP(w, req)
				return
			}

			depth := hookDepth(req)
			if depth >= MaxHookDepth {
				logrus.WithFields(logrus.Fields{
					"component": "model_hook",
					"table":     target.Table,
					"operation": string(target.Operation),
					"depth":     depth,
				}).Error("hook chain too deep; refusing the write")
				// Refused, not run without its hooks: a validation hook that stopped running must not
				// let writes through. 508 is the honest status — the request is well formed and the
				// server detected a loop while answering it.
				utilities.WriteJSON(w, http.StatusLoopDetected, map[string]string{
					"message": "This write is too many hooks deep, so it was not applied",
				})
				return
			}

			log := logrus.WithFields(logrus.Fields{
				"component": "model_hook",
				"table":     target.Table,
				"operation": string(target.Operation),
			})

			body, ok := readBody(w, req, maxBody, log)
			if !ok {
				return
			}

			// Field validators run before the hook. A hook may rewrite the body, and a validator
			// that ran afterwards would be judging values the author never sent, while one that
			// runs first judges exactly what arrived.
			if len(target.Validators) > 0 && !runValidators(w, req, target, body, opts, log, depth) {
				return
			}

			if target.Before != nil {
				outcome, proceed := runBefore(w, req, target, body, opts, log, depth)
				if !proceed {
					return
				}
				if outcome.Kind == OutcomeReplace {
					body = outcome.Body
				}
			}

			// Restore the body for the proxy, replaced or not.
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
			req.Header.Set("Content-Length", strconv.Itoa(len(body)))

			if target.After == nil {
				next.ServeHTTP(w, req)
				return
			}

			rec := &recorder{ResponseWriter: w, captureBody: wantsRepresentation(req)}
			next.ServeHTTP(rec, req)
			runAfter(req, target, rec, opts, log, depth)
		})
	}
}

// classifyFor adapts the caller-supplied hook map into what Classify needs.
func classifyFor(req *http.Request, hooks HooksFunc, validators ValidatorsFunc) Target {
	var declared map[string]TableHooksView
	if hooks != nil {
		declared = hooks(req)
	}
	var fields map[string]TableValidatorsView
	if validators != nil {
		fields = validators(req)
	}
	if declared == nil && fields == nil {
		return Target{}
	}
	return classifyWith(req, declared, fields)
}

// readBody buffers the request body under the cap.
//
// Refusing an oversized body is deliberate: the alternative is calling the hook without it (a lie) or
// skipping the hook (a silent hole).
func readBody(w http.ResponseWriter, req *http.Request, max int64, log *logrus.Entry) ([]byte, bool) {
	if req.Body == nil {
		return nil, true
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, max+1))
	if err != nil {
		log.WithError(err).Warn("could not read request body for a hooked write")
		utilities.WriteJSON(w, http.StatusBadRequest, map[string]string{"message": "Could not read request body"})
		return nil, false
	}
	if int64(len(body)) > max {
		log.WithField("limit", max).Warn("request body too large to show a hook")
		utilities.WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"message": "Request body is larger than this table's hook can be shown",
		})
		return nil, false
	}
	return body, true
}

// runBefore calls the before hook and applies its answer. The bool reports whether to continue.
func runBefore(
	w http.ResponseWriter,
	req *http.Request,
	target Target,
	body []byte,
	opts Options,
	log *logrus.Entry,
	depth int,
) (Outcome, bool) {
	cfg := *target.Before
	view := HookConfigView{TimeoutMs: cfg.TimeoutMs, OnUnavailable: cfg.OnUnavailable}
	log = log.WithFields(logrus.Fields{"event": target.BeforeEvent, "function": cfg.Function})

	url, err := opts.ResolveURL(req, cfg.Function)
	if err != nil {
		return unavailable(w, view, target.BeforeEvent, "resolving the function URL: "+err.Error(), log)
	}

	encoded, err := json.Marshal(buildPayload(req, target, body, opts))
	if err != nil {
		return unavailable(w, view, target.BeforeEvent, "encoding the hook payload: "+err.Error(), log)
	}

	outcome := opts.Dispatcher.Call(req.Context(), url, target.BeforeEvent, view, encoded, depth+1)
	switch outcome.Kind {
	case OutcomeReject:
		// The hook's own status and message. Nothing is added: a hook that chose 409 means 409.
		log.WithField("status", outcome.Status).Info("hook rejected the write")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(outcome.Status)
		if len(outcome.Body) > 0 {
			_, _ = w.Write(outcome.Body)
		}
		return outcome, false

	case OutcomeUnavailable:
		return unavailable(w, view, target.BeforeEvent, outcome.Reason, log)

	default:
		return outcome, true
	}
}

// unavailable applies the per-hook policy for a hook that did not answer.
func unavailable(
	w http.ResponseWriter,
	view HookConfigView,
	event string,
	reason string,
	log *logrus.Entry,
) (Outcome, bool) {
	if view.RejectsWhenUnavailable(event) {
		log.WithField("reason", reason).Error("hook unavailable; refusing the write")
		// 503, not the hook's silence dressed as a validation failure: the caller's request was fine
		// and retrying it later may work, which is exactly what this status says.
		utilities.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{
			"message": "A hook for this table could not be reached, so the write was not applied",
		})
		return Outcome{Kind: OutcomeUnavailable, Reason: reason}, false
	}
	log.WithField("reason", reason).Warn("hook unavailable; allowing the write")
	return Outcome{Kind: OutcomeProceed}, true
}

// runAfter fires the after hook once the client already has its answer.
func runAfter(
	req *http.Request,
	target Target,
	rec *recorder,
	opts Options,
	log *logrus.Entry,
	depth int,
) {
	if rec.status < 200 || rec.status >= 300 {
		// The write did not happen, so there is nothing to react to.
		return
	}
	cfg := *target.After
	view := HookConfigView{TimeoutMs: cfg.TimeoutMs, OnUnavailable: cfg.OnUnavailable}
	log = log.WithFields(logrus.Fields{"event": target.AfterEvent, "function": cfg.Function})

	url, err := opts.ResolveURL(req, cfg.Function)
	if err != nil {
		log.WithError(err).Warn("after hook not called: could not resolve the function URL")
		return
	}

	body := map[string]any{
		"table":     target.Table,
		"operation": string(target.Operation),
		"requestId": requestID(req, opts),
		"user":      claimsFor(req, opts),
	}
	if rec.captureBody && rec.body.Len() > 0 {
		body["rows"] = json.RawMessage(rec.body.Bytes())
	}
	if target.Operation == OpDelete || target.Operation == OpUpdate {
		body["filter"] = req.URL.RawQuery
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		log.WithError(err).Warn("after hook not called: could not encode the payload")
		return
	}

	// Detached from the request context on purpose. The client has its response, and cancelling the
	// hook because the connection closed would drop side effects the write already earned.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), afterHookTimeout)
	defer cancel()

	outcome := opts.Dispatcher.Call(ctx, url, target.AfterEvent, view, encoded, depth+1)
	switch outcome.Kind {
	case OutcomeReject:
		// An after hook cannot undo a committed write, so a rejection is only a log line — and worth
		// one, because a hook returning 4xx here probably expected to be able to stop something.
		log.WithField("status", outcome.Status).Warn("after hook rejected, but the write already happened")
	case OutcomeUnavailable:
		log.WithField("reason", outcome.Reason).Warn("after hook unavailable")
	}
}

func buildPayload(req *http.Request, target Target, body []byte, opts Options) payload {
	p := payload{
		Table:     target.Table,
		Operation: target.Operation,
		RequestID: requestID(req, opts),
		User:      claimsFor(req, opts),
	}
	switch target.Operation {
	case OpInsert:
		p.Rows = rowsPayload(body)
	case OpUpdate:
		p.Patch = json.RawMessage(body)
		p.Filter = req.URL.RawQuery
	case OpDelete:
		p.Filter = req.URL.RawQuery
	}
	if opts.Callback != nil {
		p.PreviousPath = opts.Callback.Path(target.Operation, target.Table, req.URL.RawQuery)
	}
	return p
}

// rowsPayload always presents an insert as an array, so a handler never has to branch on whether it
// received one row or several.
func rowsPayload(body []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return json.RawMessage("[]")
	}
	if trimmed[0] == '[' {
		return json.RawMessage(trimmed)
	}
	return json.RawMessage(append(append([]byte("["), trimmed...), ']'))
}

func requestID(req *http.Request, opts Options) string {
	if opts.RequestID != nil {
		return opts.RequestID(req)
	}
	return req.Header.Get("X-Request-Id")
}

func claimsFor(req *http.Request, opts Options) *Claims {
	if opts.Claims == nil {
		return nil
	}
	return opts.Claims(req)
}

// wantsRepresentation reports whether the caller asked PostgREST to return the written rows, which is
// the only case where an after hook can be shown them.
func wantsRepresentation(req *http.Request) bool {
	for _, prefer := range req.Header.Values("Prefer") {
		if strings.Contains(prefer, "return=representation") {
			return true
		}
	}
	return false
}

// recorder passes the proxy's response through while noting its status, and its body only when an
// after hook can actually be shown it.
type recorder struct {
	http.ResponseWriter
	status      int
	captureBody bool
	body        bytes.Buffer
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.captureBody {
		// Bounded by the same cap as a request body: a hook shown a 40MB result set is a memory
		// problem, not a feature.
		if int64(r.body.Len()) < MaxBodyBytes {
			r.body.Write(p)
		}
	}
	return r.ResponseWriter.Write(p)
}
