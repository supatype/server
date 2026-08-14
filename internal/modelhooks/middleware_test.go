package modelhooks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stack wires a hook function, the middleware, and a stand-in for PostgREST.
type stack struct {
	server   *httptest.Server
	upstream *httptest.Server
	// upstreamBody is what the proxy received, which is how a replaced body is observed.
	upstreamBody   func() string
	upstreamCalled func() bool
	hookPayloads   func() []map[string]any
	hookEvents     func() []string
}

func newStack(t *testing.T, hooks map[string]TableHooksView, hookFn http.HandlerFunc, upstream http.HandlerFunc) *stack {
	t.Helper()

	var mu sync.Mutex
	var payloads []map[string]any
	var events []string

	fnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		mu.Lock()
		payloads = append(payloads, parsed)
		events = append(events, r.Header.Get("X-Supatype-Hook"))
		mu.Unlock()
		hookFn(w, r)
	}))
	t.Cleanup(fnServer.Close)

	var upstreamBody string
	var called bool
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		upstreamBody = string(raw)
		called = true
		mu.Unlock()
		if upstream != nil {
			upstream(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(upstreamServer.Close)

	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequest(r.Method, upstreamServer.URL, r.Body)
		res, err := upstreamServer.Client().Do(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		w.WriteHeader(res.StatusCode)
		_, _ = w.Write(body)
	})

	mw := Middleware(Options{
		Dispatcher: NewDispatcher(nil, ""),
		Hooks:      func(*http.Request) map[string]TableHooksView { return hooks },
		ResolveURL: func(*http.Request, string) (string, error) { return fnServer.URL, nil },
		Claims: func(*http.Request) *Claims {
			return &Claims{Sub: "user-1", Role: "authenticated"}
		},
		RequestID: func(*http.Request) string { return "req-1" },
	})

	server := httptest.NewServer(mw(proxy))
	t.Cleanup(server.Close)

	return &stack{
		server:       server,
		upstream:     upstreamServer,
		upstreamBody: func() string { mu.Lock(); defer mu.Unlock(); return upstreamBody },
		upstreamCalled: func() bool {
			mu.Lock()
			defer mu.Unlock()
			return called
		},
		hookPayloads: func() []map[string]any { mu.Lock(); defer mu.Unlock(); return payloads },
		hookEvents:   func() []string { mu.Lock(); defer mu.Unlock(); return events },
	}
}

func beforeHooks(fn string, extra ...HookConfigEntry) map[string]TableHooksView {
	view := TableHooksView{EventBeforeChange: HookConfigEntry{Function: fn}}
	if len(extra) > 0 {
		view[EventBeforeChange] = extra[0]
	}
	return map[string]TableHooksView{"posts": view}
}

func do(t *testing.T, s *stack, method, path, body string, headers ...string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, s.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	res, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func TestUnhookedRequestPassesStraightThrough(t *testing.T) {
	s := newStack(t, beforeHooks("moderate"), func(w http.ResponseWriter, _ *http.Request) {
		t.Error("hook called for a table with no hooks")
	}, nil)

	res := do(t, s, http.MethodPost, "/comments", `{"body":"hi"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}
	// The body must survive untouched: this path must not buffer or rewrite anything.
	if s.upstreamBody() != `{"body":"hi"}` {
		t.Fatalf("upstream body = %q", s.upstreamBody())
	}
}

func TestBeforeHookCanReplaceTheBody(t *testing.T) {
	s := newStack(t, beforeHooks("moderate"), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"rows":[{"title":"trimmed"}]}`))
	}, nil)

	do(t, s, http.MethodPost, "/posts", `{"title":"  untrimmed  "}`)

	if got := s.upstreamBody(); got != `[{"title":"trimmed"}]` {
		t.Fatalf("upstream body = %q, want the hook's rows", got)
	}
}

func TestBeforeHookRejectionStopsTheWrite(t *testing.T) {
	s := newStack(t, beforeHooks("moderate"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"Already exists","code":"dupe"}`))
	}, nil)

	res := do(t, s, http.MethodPost, "/posts", `{"title":"x"}`)

	// The status the hook chose reaches the caller, not a flattened 422.
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "Already exists") {
		t.Fatalf("body = %s, want the hook's message", body)
	}
	// The point of a before hook: the write must not have happened.
	if s.upstreamCalled() {
		t.Fatal("the write reached the database despite a rejection")
	}
}

func TestBeforeHookUnavailableRefusesByDefault(t *testing.T) {
	s := newStack(t, beforeHooks("moderate"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}, nil)

	res := do(t, s, http.MethodPost, "/posts", `{"title":"x"}`)

	// 503 rather than the hook's silence dressed as a validation failure: the request was fine and
	// retrying may work, which is what this status says.
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.StatusCode)
	}
	if s.upstreamCalled() {
		t.Fatal("a validation hook that could not be reached let the write through")
	}
}

func TestBeforeHookUnavailableCanBeConfiguredToAllow(t *testing.T) {
	hooks := beforeHooks("moderate", HookConfigEntry{Function: "moderate", OnUnavailable: "log"})
	s := newStack(t, hooks, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}, nil)

	res := do(t, s, http.MethodPost, "/posts", `{"title":"x"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want the write to proceed", res.StatusCode)
	}
	if !s.upstreamCalled() {
		t.Fatal("onUnavailable=log did not allow the write")
	}
}

func TestInsertPayloadIsAlwaysAnArray(t *testing.T) {
	// So a handler never branches on whether one row or several were submitted.
	s := newStack(t, beforeHooks("moderate"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, nil)

	do(t, s, http.MethodPost, "/posts", `{"title":"one"}`)

	payloads := s.hookPayloads()
	if len(payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(payloads))
	}
	rows, ok := payloads[0]["rows"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("rows = %#v, want a one-element array", payloads[0]["rows"])
	}
	if payloads[0]["user"] == nil || payloads[0]["requestId"] != "req-1" {
		t.Fatalf("payload lost its identity fields: %#v", payloads[0])
	}
}

func TestUpdatePayloadCarriesThePatchAndFilter(t *testing.T) {
	s := newStack(t, beforeHooks("moderate"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, nil)

	do(t, s, http.MethodPatch, "/posts?status=eq.draft", `{"title":"x"}`)

	p := s.hookPayloads()[0]
	if p["operation"] != "update" {
		t.Fatalf("operation = %v, want update", p["operation"])
	}
	if p["filter"] != "status=eq.draft" {
		t.Fatalf("filter = %v, want the request's query", p["filter"])
	}
	if p["rows"] != nil {
		t.Fatalf("an update carries a patch, not rows: %#v", p)
	}
}

func TestAfterHookRunsOnlyWhenTheWriteSucceeded(t *testing.T) {
	hooks := map[string]TableHooksView{
		"posts": {EventAfterChange: HookConfigEntry{Function: "reindex"}},
	}

	t.Run("success", func(t *testing.T) {
		s := newStack(t, hooks, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
		})
		do(t, s, http.MethodPost, "/posts", `{"title":"x"}`)
		if events := s.hookEvents(); len(events) != 1 || events[0] != EventAfterChange {
			t.Fatalf("events = %v, want one afterChange", events)
		}
	})

	t.Run("failed write", func(t *testing.T) {
		// Nothing happened, so there is nothing to react to — and calling the hook would tell it a
		// row exists when it does not.
		s := newStack(t, hooks, func(w http.ResponseWriter, _ *http.Request) {
			t.Error("after hook called for a failed write")
		}, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		})
		do(t, s, http.MethodPost, "/posts", `{"title":"x"}`)
	})
}

func TestAfterHookSeesRowsOnlyWithRepresentation(t *testing.T) {
	hooks := map[string]TableHooksView{
		"posts": {EventAfterChange: HookConfigEntry{Function: "reindex"}},
	}
	written := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`[{"id":"1","title":"x"}]`))
	}

	t.Run("with Prefer", func(t *testing.T) {
		s := newStack(t, hooks, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}, written)
		do(t, s, http.MethodPost, "/posts", `{"title":"x"}`, "Prefer", "return=representation")

		p := s.hookPayloads()[0]
		if p["rows"] == nil {
			t.Fatalf("rows missing when the caller asked for them: %#v", p)
		}
	})

	t.Run("without Prefer", func(t *testing.T) {
		// PostgREST returns no body, so no amount of server work invents one. The context type says
		// rows are optional for exactly this reason.
		s := newStack(t, hooks, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}, written)
		do(t, s, http.MethodPost, "/posts", `{"title":"x"}`)

		// Key *absence*, not a nil value. `rows: null` would decode to nil here too, and the
		// generated context types distinguish them: `rows?: Row[]` means undefined, and a handler
		// testing `ctx.rows === undefined` would be misled by an explicit null.
		p := s.hookPayloads()[0]
		if _, present := p["rows"]; present {
			t.Fatalf("rows present without return=representation: %#v", p)
		}
	})
}

func TestAfterHookFailureDoesNotFailTheWrite(t *testing.T) {
	hooks := map[string]TableHooksView{
		"posts": {EventAfterChange: HookConfigEntry{Function: "reindex"}},
	}
	s := newStack(t, hooks, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}, nil)

	res := do(t, s, http.MethodPost, "/posts", `{"title":"x"}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d — an after hook must not undo a committed write", res.StatusCode)
	}
}

func TestOversizedBodyIsRefusedRatherThanUnhooked(t *testing.T) {
	// The alternative is calling the hook without the body (a lie) or skipping it (a silent hole).
	var mu sync.Mutex
	called := false
	fn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		called = true
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(fn.Close)

	mw := Middleware(Options{
		Dispatcher:   NewDispatcher(nil, ""),
		Hooks:        func(*http.Request) map[string]TableHooksView { return beforeHooks("moderate") },
		ResolveURL:   func(*http.Request, string) (string, error) { return fn.URL, nil },
		MaxBodyBytes: 16,
	})
	server := httptest.NewServer(mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an oversized body reached the database with its hook skipped")
	})))
	t.Cleanup(server.Close)

	res, err := server.Client().Post(server.URL+"/posts", "application/json",
		strings.NewReader(`{"title":"a body well over the configured cap"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", res.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if called {
		t.Fatal("the hook was called without the body it was supposed to see")
	}
}

func TestSlowUpstreamStillReturnsTheHookVerdictPromptly(t *testing.T) {
	// Guards the ordering: the before hook runs to completion before the proxy is touched at all.
	s := newStack(t, beforeHooks("moderate"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"no"}`))
	}, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusCreated)
	})

	start := time.Now()
	res := do(t, s, http.MethodPost, "/posts", `{"title":"x"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("rejection took %v — the upstream was contacted anyway", elapsed)
	}
}
