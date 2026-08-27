package modelhooks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Per-field validators, run on the same interception as `beforeChange`.
//
// What is asserted here is mostly about *not* running them: on a delete, on a row that does not
// mention the field, on a read. A validator that fires when it should not is a write refused for no
// stated reason, and the caller has nothing to act on.

type validatorStack struct {
	server   *httptest.Server
	payloads func() []validatorPayload
	upstream func() bool
}

func newValidatorStack(
	t *testing.T,
	validators map[string]TableValidatorsView,
	fn http.HandlerFunc,
) *validatorStack {
	t.Helper()

	var mu sync.Mutex
	var seen []validatorPayload
	var upstreamCalled bool

	fnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var parsed validatorPayload
		_ = json.Unmarshal(raw, &parsed)
		mu.Lock()
		seen = append(seen, parsed)
		mu.Unlock()
		fn(w, r)
	}))
	t.Cleanup(fnServer.Close)

	proxy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		upstreamCalled = true
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	})

	mw := Middleware(Options{
		Dispatcher: NewDispatcher(nil, ""),
		Validators: func(*http.Request) map[string]TableValidatorsView { return validators },
		ResolveURL: func(*http.Request, string) (string, error) { return fnServer.URL, nil },
		Claims:     func(*http.Request) *Claims { return &Claims{Sub: "user-1"} },
		RequestID:  func(*http.Request) string { return "req-1" },
	})

	server := httptest.NewServer(mw(proxy))
	t.Cleanup(server.Close)

	return &validatorStack{
		server:   server,
		payloads: func() []validatorPayload { mu.Lock(); defer mu.Unlock(); return seen },
		upstream: func() bool { mu.Lock(); defer mu.Unlock(); return upstreamCalled },
	}
}

func postToStack(t *testing.T, s *validatorStack, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatalf("sending request: %v", err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func onlyValidator(field, function string) map[string]TableValidatorsView {
	return map[string]TableValidatorsView{
		"products": {field: HookConfigEntry{Function: function, TimeoutMs: 1000}},
	}
}

func TestValidatorReceivesOnlyItsOwnFieldValue(t *testing.T) {
	s := newValidatorStack(t, onlyValidator("setup_items", "check-items"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	postToStack(t, s, "/products", `[{"setup_items":[{"label":"a"}],"name":"ignored"}]`)

	payloads := s.payloads()
	if len(payloads) != 1 {
		t.Fatalf("expected one validator call, got %d", len(payloads))
	}
	if payloads[0].Field != "setup_items" {
		t.Errorf("payload should name the field being validated, got %q", payloads[0].Field)
	}
	// The value, not the row: a validator handed the whole row would answer a different question
	// from the one its refusal gets attributed to.
	if string(payloads[0].Value) != `[{"label":"a"}]` {
		t.Errorf("payload should carry the field's value alone, got %s", payloads[0].Value)
	}
	if !s.upstream() {
		t.Error("an accepted value must reach the database")
	}
}

func TestValidatorRejectionNamesTheField(t *testing.T) {
	s := newValidatorStack(t, onlyValidator("setup_items", "check-items"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"At least one setup item is required."}`))
	})

	res := postToStack(t, s, "/products", `[{"setup_items":[]}]`)

	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", res.StatusCode)
	}
	var body rejectionBody
	raw, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("rejection should be JSON: %v (%s)", err, raw)
	}
	// The field travels separately from the prose, so a client does not parse a sentence to know
	// which input to mark.
	if body.Field != "setup_items" {
		t.Errorf("rejection should name the field, got %q", body.Field)
	}
	if body.Message != "At least one setup item is required." {
		t.Errorf("the validator's own message must survive, got %q", body.Message)
	}
	if s.upstream() {
		t.Error("a rejected write must not reach the database")
	}
}

func TestValidatorIsSkippedWhenTheRowOmitsTheField(t *testing.T) {
	// A patch that does not mention the column is not changing it. Validating an absent value would
	// refuse writes that touch an unrelated field.
	s := newValidatorStack(t, onlyValidator("setup_items", "check-items"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})

	postToStack(t, s, "/products", `[{"name":"only the name"}]`)

	if len(s.payloads()) != 0 {
		t.Errorf("a row omitting the field must not call its validator, got %d calls", len(s.payloads()))
	}
	if !s.upstream() {
		t.Error("the write should have proceeded")
	}
}

func TestValidatorRunsForEveryRowInABatch(t *testing.T) {
	// Validating the first row and trusting the rest would be worse than not validating at all: the
	// rule would appear to hold and silently not.
	var calls int
	var mu sync.Mutex
	s := newValidatorStack(t, onlyValidator("sku", "check-sku"), func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	postToStack(t, s, "/products", `[{"sku":"AAA"},{"sku":"BBB"},{"sku":"CCC"}]`)

	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Errorf("expected one call per row, got %d", calls)
	}
}

func TestValidatorDoesNotRunOnDelete(t *testing.T) {
	// A delete carries no field values, so there is nothing for a per-field rule to judge.
	s := newValidatorStack(t, onlyValidator("sku", "check-sku"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})

	req, _ := http.NewRequest(http.MethodDelete, s.server.URL+"/products?id=eq.1", nil)
	res, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatalf("sending request: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	if len(s.payloads()) != 0 {
		t.Errorf("a delete must not call field validators, got %d calls", len(s.payloads()))
	}
	if res.StatusCode != http.StatusCreated {
		t.Errorf("the delete should have proceeded, got %d", res.StatusCode)
	}
}

func TestValidatorUnavailableRefusesTheWriteByDefault(t *testing.T) {
	// A validator that cannot be reached has not approved anything. Letting the write through would
	// accept a value nobody checked, which is the failure this feature exists to prevent.
	s := newValidatorStack(t, onlyValidator("sku", "check-sku"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	res := postToStack(t, s, "/products", `[{"sku":"AAA"}]`)

	if res.StatusCode < 500 {
		t.Errorf("an unreachable validator must refuse the write, got %d", res.StatusCode)
	}
	if s.upstream() {
		t.Error("the write must not have reached the database")
	}
}

func TestValidatorsRunInAStableOrder(t *testing.T) {
	// Map iteration is random in Go. A row breaching two rules would otherwise be refused by a
	// different one on each request, making the error a coin toss.
	validators := map[string]TableValidatorsView{
		"products": {
			"zeta":  HookConfigEntry{Function: "check-zeta", TimeoutMs: 1000},
			"alpha": HookConfigEntry{Function: "check-alpha", TimeoutMs: 1000},
		},
	}
	for attempt := 0; attempt < 5; attempt++ {
		s := newValidatorStack(t, validators, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
		})
		res := postToStack(t, s, "/products", `[{"alpha":1,"zeta":2}]`)
		var body rejectionBody
		raw, _ := io.ReadAll(res.Body)
		_ = json.Unmarshal(raw, &body)
		if body.Field != "alpha" {
			t.Fatalf("attempt %d: expected the first field alphabetically, got %q", attempt, body.Field)
		}
	}
}
