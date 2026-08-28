package modelhooks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/proxy"
)

// The paths a hooked write takes when something is missing or broken. A hook
// that was declared and did not run is the failure that matters: it means a
// write the schema said to validate arrived unvalidated.

// ─── Who the hook is told about ───────────────────────────────────────────────

const hookSecret = "hook-jwt-secret"

// A hook that trusts `user.sub` must be handed a verified subject or none at
// all. Everything that is not a token this project issued is anonymous.
func TestClaimsFromBearer(t *testing.T) {
	sign := func(t *testing.T, secret string, method jwt.SigningMethod, claims jwt.MapClaims) string {
		t.Helper()
		signed, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}

	valid := sign(t, hookSecret, jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-1", "role": "authenticated", "email": "a@example.com",
	})

	for name, tc := range map[string]struct {
		secret string
		header string
		want   *Claims
	}{
		"a token this project issued": {
			hookSecret, "Bearer " + valid,
			&Claims{Sub: "user-1", Role: "authenticated", Email: "a@example.com"},
		},
		"oddly cased scheme": {
			hookSecret, "bearer " + valid,
			&Claims{Sub: "user-1", Role: "authenticated", Email: "a@example.com"},
		},
		"no secret configured":        {"  ", "Bearer " + valid, nil},
		"no header":                   {hookSecret, "", nil},
		"not a bearer":                {hookSecret, "Basic " + valid, nil},
		"a bearer with nothing after": {hookSecret, "Bearer ", nil},
		"a header too short":          {hookSecret, "Bear", nil},
		"not a token":                 {hookSecret, "Bearer nonsense", nil},
		"signed by someone else": {
			hookSecret,
			"Bearer " + sign(t, "another-secret", jwt.SigningMethodHS256, jwt.MapClaims{"sub": "user-1"}),
			nil,
		},
		"signed with another algorithm": {
			hookSecret,
			"Bearer " + sign(t, hookSecret, jwt.SigningMethodHS512, jwt.MapClaims{"sub": "user-1"}),
			nil,
		},
		// The anon and service-role keys carry a role and no subject, so they
		// arrive as an anonymous caller rather than a user with an empty id.
		"a key with no subject": {
			hookSecret,
			"Bearer " + sign(t, hookSecret, jwt.SigningMethodHS256, jwt.MapClaims{"role": "anon"}),
			nil,
		},
	} {
		req := httptest.NewRequest(http.MethodPost, "/posts", nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}

		got := ClaimsFromBearer(tc.secret)(req)
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("%s: got %+v, want an anonymous caller", name, got)
		case tc.want != nil && got == nil:
			t.Errorf("%s: got nothing, want %+v", name, tc.want)
		case tc.want != nil && *got != *tc.want:
			t.Errorf("%s: got %+v, want %+v", name, got, tc.want)
		}
	}
}

// A token with no role or email still names its subject: those fields are
// optional and their absence is not a reason to treat the caller as anonymous.
func TestATokenWithOnlyASubject(t *testing.T) {
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "user-1"}).
		SignedString([]byte(hookSecret))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/posts", nil)
	req.Header.Set("Authorization", "Bearer "+signed)

	got := ClaimsFromBearer(hookSecret)(req)
	if got == nil || got.Sub != "user-1" || got.Role != "" {
		t.Errorf("claims = %+v", got)
	}
}

// ─── The validator catalogue ──────────────────────────────────────────────────

// The validator view is what a request is classified against, so it has to
// carry every field's configuration through.
func TestValidatorsViewFromManifest(t *testing.T) {
	if got := ValidatorViewsFromManifest(nil); got != nil {
		t.Errorf("nothing declared should be nothing at all, got %v", got)
	}
	if got := ValidatorViewsFromManifest(map[string]proxy.TableValidators{}); got != nil {
		t.Errorf("an empty map should be nothing at all, got %v", got)
	}

	views := ValidatorViewsFromManifest(map[string]proxy.TableValidators{
		"posts": {
			"title": proxy.HookConfig{Function: "check-title", TimeoutMs: 250, OnUnavailable: "log"},
			"body":  proxy.HookConfig{Function: "check-body"},
		},
	})
	fields, ok := views["posts"]
	if !ok {
		t.Fatalf("views = %+v", views)
	}
	if fields["title"].Function != "check-title" || fields["title"].TimeoutMs != 250 ||
		fields["title"].OnUnavailable != "log" {
		t.Errorf("title = %+v", fields["title"])
	}
	if fields["body"].Function != "check-body" {
		t.Errorf("body = %+v", fields["body"])
	}
}

// A field with no function is not a validator, so it is not one a write waits
// on.
func TestAValidatorWithNoFunctionIsNotOne(t *testing.T) {
	target := classifyFor(
		httptest.NewRequest(http.MethodPost, "/posts", nil),
		func(*http.Request) map[string]TableHooksView { return nil },
		func(*http.Request) map[string]TableValidatorsView {
			return map[string]TableValidatorsView{"posts": {"title": HookConfigEntry{}}}
		},
	)
	if target.HasWork() {
		t.Errorf("target = %+v, want no work", target)
	}
}

// A request for a table nobody declared anything about is not hook work, and
// must be handed through untouched.
func TestNothingDeclaredIsNoWork(t *testing.T) {
	target := classifyFor(
		httptest.NewRequest(http.MethodPost, "/posts", nil),
		func(*http.Request) map[string]TableHooksView { return nil },
		func(*http.Request) map[string]TableValidatorsView { return nil },
	)
	if target.HasWork() {
		t.Errorf("target = %+v, want no work", target)
	}
}

// ─── Bodies ───────────────────────────────────────────────────────────────────

// A body that is neither a list of rows nor a single one has no values to
// validate. An insert may carry several rows and a patch carries one, and a
// validator that ran on the first of a batch and not the rest would be worse
// than one that did not run at all.
func TestFieldValues(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"a single row":            {`{"title":"a"}`, 1},
		"several rows":            {`[{"title":"a"},{"title":"b"}]`, 2},
		"a row without the field": {`{"body":"a"}`, 0},
		"one of several without":  {`[{"title":"a"},{"body":"b"}]`, 1},
		"not JSON at all":         {`{not json`, 0},
		"a bare string":           {`"just a string"`, 0},
		"a list of scalars":       {`[1,2,3]`, 0},
	} {
		if got := fieldValues([]byte(tc.body), "title"); len(got) != tc.want {
			t.Errorf("%s: %d values, want %d", name, len(got), tc.want)
		}
	}
}

// A request with no body at all is not a failure to read one.
func TestReadingAnAbsentBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/posts", nil)
	req.Body = nil

	body, ok := readBody(httptest.NewRecorder(), req, MaxBodyBytes, logrus.NewEntry(logrus.New()))
	if !ok {
		t.Error("an absent body was treated as unreadable")
	}
	if body != nil {
		t.Errorf("body = %q, want nothing", body)
	}
}

// A body that fails mid-read is refused rather than passed on truncated: a hook
// shown half a write would validate the wrong thing.
func TestABodyThatFailsMidRead(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/posts", nil)
	req.Body = io.NopCloser(failingReader{})

	rec := httptest.NewRecorder()
	if _, ok := readBody(rec, req, MaxBodyBytes, logrus.NewEntry(logrus.New())); ok {
		t.Fatal("an unreadable body was accepted")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// ─── Dispatch ─────────────────────────────────────────────────────────────────

// A URL that cannot be made into a request is a hook that did not run, which is
// unavailable rather than a rejection: the two mean different things to the
// unavailability policy.
func TestDispatchingToAURLThatIsNotOne(t *testing.T) {
	outcome := NewDispatcher(nil, "").Call(context.Background(), "://not a url",
		EventBeforeChange, HookConfigView{TimeoutMs: 1000}, []byte(`{}`), 0)
	if outcome.Kind != OutcomeUnavailable {
		t.Errorf("outcome = %+v, want unavailable", outcome)
	}
}

// A verdict body that fails mid-read is the same: the hook did not answer.
func TestAVerdictThatCannotBeRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
		// Closing without writing the promised bytes makes the read fail.
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, _, err := hijacker.Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}
	}))
	defer server.Close()

	outcome := NewDispatcher(nil, "").Call(context.Background(), server.URL,
		EventBeforeChange, HookConfigView{TimeoutMs: 2000}, []byte(`{}`), 0)
	if outcome.Kind != OutcomeUnavailable {
		t.Errorf("outcome = %+v, want unavailable", outcome)
	}
}

// ─── What a hook is shown ─────────────────────────────────────────────────────

// An update and a delete are identified by their filter, so a hook can see
// which rows are about to change.
func TestTheFilterReachesTheHook(t *testing.T) {
	for name, tc := range map[string]struct {
		method string
		event  string
		body   string
	}{
		// A delete has its own event names, so a hook declared for a change is
		// not one a delete runs.
		"an update": {http.MethodPatch, EventBeforeChange, `{"title":"new"}`},
		"a delete":  {http.MethodDelete, EventBeforeDelete, ""},
	} {
		s := newStack(t, map[string]TableHooksView{
			"posts": {tc.event: HookConfigEntry{Function: "check"}},
		}, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}, nil)

		res := do(t, s, tc.method, "/posts?id=eq.1", tc.body)
		_ = res.Body.Close()

		payloads := s.hookPayloads()
		if len(payloads) != 1 {
			t.Fatalf("%s: payloads = %+v", name, payloads)
		}
		if payloads[0]["filter"] != "id=eq.1" {
			t.Errorf("%s: filter = %v, want the query the write names", name, payloads[0]["filter"])
		}
	}
}

// The after hook is told the filter too, so it can report on what it changed.
func TestTheAfterHookIsToldTheFilter(t *testing.T) {
	s := newStack(t, map[string]TableHooksView{
		"posts": {EventAfterChange: HookConfigEntry{Function: "notify"}},
	}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	res := do(t, s, http.MethodPatch, "/posts?id=eq.7", `{"title":"new"}`)
	_ = res.Body.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if payloads := s.hookPayloads(); len(payloads) > 0 {
			if payloads[0]["filter"] != "id=eq.7" {
				t.Errorf("filter = %v", payloads[0]["filter"])
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the after hook was never called")
}

// ─── When the hook cannot be reached at all ───────────────────────────────────

// A function URL that cannot be resolved is a before hook that did not run, so
// the unavailability policy decides — the write is not simply let through.
func TestABeforeHookWhoseURLWillNotResolve(t *testing.T) {
	var upstreamCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusCreated)
	})

	mw := Middleware(Options{
		Dispatcher: NewDispatcher(nil, ""),
		Hooks: func(*http.Request) map[string]TableHooksView {
			return map[string]TableHooksView{"posts": {EventBeforeChange: HookConfigEntry{Function: "check"}}}
		},
		ResolveURL: func(*http.Request, string) (string, error) {
			return "", errors.New("no worker for that function")
		},
		RequestID: func(*http.Request) string { return "req-1" },
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"title":"x"}`))
	mw(next).ServeHTTP(rec, req)

	if upstreamCalled {
		t.Error("the write went through with its hook unrun")
	}
	if rec.Code == http.StatusCreated {
		t.Errorf("status = %d, want a refusal", rec.Code)
	}
}

// An after hook whose URL will not resolve is a log line: the write has already
// happened and cannot be undone.
func TestAnAfterHookWhoseURLWillNotResolve(t *testing.T) {
	var upstreamCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusCreated)
	})

	mw := Middleware(Options{
		Dispatcher: NewDispatcher(nil, ""),
		Hooks: func(*http.Request) map[string]TableHooksView {
			return map[string]TableHooksView{"posts": {EventAfterChange: HookConfigEntry{Function: "notify"}}}
		},
		ResolveURL: func(*http.Request, string) (string, error) {
			return "", errors.New("no worker for that function")
		},
		RequestID: func(*http.Request) string { return "req-1" },
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"title":"x"}`))
	mw(next).ServeHTTP(rec, req)

	if !upstreamCalled {
		t.Error("the write was blocked by an after hook")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want the upstream's answer", rec.Code)
	}
}

// A rejection from an after hook cannot undo a committed write, so it is a log
// line rather than an error the caller sees.
func TestAnAfterHookRejectionCannotUndoTheWrite(t *testing.T) {
	s := newStack(t, map[string]TableHooksView{
		"posts": {EventAfterChange: HookConfigEntry{Function: "notify"}},
	}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"too late"}`))
	}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	res := do(t, s, http.MethodPost, "/posts", `{"title":"x"}`)
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want the write to stand", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if strings.Contains(string(body), "too late") {
		t.Errorf("the after hook's refusal reached the caller: %s", body)
	}
}

// ─── The previous() path ──────────────────────────────────────────────────────

// Without a callback there is no previous path, which the generated types model
// as absent rather than as a call that will fail.
func TestNoCallbackMeansNoPreviousPath(t *testing.T) {
	s := newStack(t, beforeHooks("check"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}, nil)

	res := do(t, s, http.MethodPost, "/posts", `{"title":"x"}`)
	_ = res.Body.Close()

	payloads := s.hookPayloads()
	if len(payloads) != 1 {
		t.Fatalf("payloads = %+v", payloads)
	}
	if _, present := payloads[0]["previousPath"]; present {
		t.Errorf("a previous path was offered with no callback to serve it: %v", payloads[0])
	}
}

// ─── Encoding ─────────────────────────────────────────────────────────────────

// A payload that cannot be encoded is a hook that did not run, and the before
// side refuses rather than letting the write past.
func TestAPayloadThatCannotBeEncoded(t *testing.T) {
	original := marshalPayload
	t.Cleanup(func() { marshalPayload = original })
	marshalPayload = func(any) ([]byte, error) { return nil, errors.New("nope") }

	var upstreamCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusCreated)
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mw := Middleware(Options{
		Dispatcher: NewDispatcher(nil, ""),
		Hooks: func(*http.Request) map[string]TableHooksView {
			return map[string]TableHooksView{"posts": {EventBeforeChange: HookConfigEntry{Function: "check"}}}
		},
		ResolveURL: func(*http.Request, string) (string, error) { return server.URL, nil },
		RequestID:  func(*http.Request) string { return "req-1" },
	})

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"a":1}`)))

	if upstreamCalled {
		t.Error("the write went through with its hook unrun")
	}
	if rec.Code == http.StatusCreated {
		t.Errorf("status = %d, want a refusal", rec.Code)
	}
}

// And the after side logs it, because the write has already happened.
func TestAnAfterPayloadThatCannotBeEncoded(t *testing.T) {
	original := marshalPayload
	t.Cleanup(func() { marshalPayload = original })
	marshalPayload = func(any) ([]byte, error) { return nil, errors.New("nope") }

	var upstreamCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusCreated)
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mw := Middleware(Options{
		Dispatcher: NewDispatcher(nil, ""),
		Hooks: func(*http.Request) map[string]TableHooksView {
			return map[string]TableHooksView{"posts": {EventAfterChange: HookConfigEntry{Function: "notify"}}}
		},
		ResolveURL: func(*http.Request, string) (string, error) { return server.URL, nil },
		RequestID:  func(*http.Request) string { return "req-1" },
	})

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"a":1}`)))

	if !upstreamCalled || rec.Code != http.StatusCreated {
		t.Errorf("the write did not stand: called = %v, status = %d", upstreamCalled, rec.Code)
	}
}

// ─── Encoding a verdict ───────────────────────────────────────────────────────

// A hook that answers 200 with nothing has said "proceed". One that answers
// with rows has said "use these instead".
func TestVerdictFromBody(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want OutcomeKind
	}{
		"nothing":                {"", OutcomeProceed},
		"only whitespace":        {"   \n ", OutcomeProceed},
		"an empty object":        {"{}", OutcomeProceed},
		"replacement rows":       {`{"rows":[{"a":1}]}`, OutcomeReplace},
		"a replacement patch":    {`{"patch":{"a":1}}`, OutcomeReplace},
		"a body nobody can read": {`{not json`, OutcomeUnavailable},
	} {
		if got := verdictFromBody([]byte(tc.body)); got.Kind != tc.want {
			t.Errorf("%s: kind = %v, want %v", name, got.Kind, tc.want)
		}
	}
}

// ─── Which paths are a table ──────────────────────────────────────────────────

// Only a bare table is hook work. An RPC returns whatever its function returns,
// and a nested path is not a PostgREST table route: running a table's hooks on
// either would be validating the wrong thing.
func TestTableFromPath(t *testing.T) {
	for name, tc := range map[string]struct{ path, want string }{
		"a table":              {"/posts", "posts"},
		"a table with a query": {"/posts?id=eq.1", "posts"},
		"no leading slash":     {"posts", "posts"},
		"an RPC call":          {"/rpc/do_thing", ""},
		"the rpc root":         {"/rpc", ""},
		"a nested path":        {"/posts/1", ""},
		"the root":             {"/", ""},
		"nothing":              {"", ""},
	} {
		if got := tableFromPath(tc.path); got != tc.want {
			t.Errorf("%s: tableFromPath(%q) = %q, want %q", name, tc.path, got, tc.want)
		}
	}
}

// ─── The request id a hook is told ────────────────────────────────────────────

// Without a resolver the request's own header is used, so a hook's logs can be
// tied back to the write that caused them.
func TestTheRequestIDFallsBackToTheHeader(t *testing.T) {
	var seen []map[string]any
	fnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		seen = append(seen, parsed)
		w.WriteHeader(http.StatusOK)
	}))
	defer fnServer.Close()

	mw := Middleware(Options{
		Dispatcher: NewDispatcher(nil, ""),
		Hooks: func(*http.Request) map[string]TableHooksView {
			return map[string]TableHooksView{"posts": {EventBeforeChange: HookConfigEntry{Function: "check"}}}
		},
		ResolveURL: func(*http.Request, string) (string, error) { return fnServer.URL, nil },
		// No RequestID resolver.
	})

	req := httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"a":1}`))
	req.Header.Set("X-Request-Id", "from-the-header")
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})).ServeHTTP(httptest.NewRecorder(), req)

	if len(seen) != 1 {
		t.Fatalf("payloads = %+v", seen)
	}
	if seen[0]["requestId"] != "from-the-header" {
		t.Errorf("request id = %v", seen[0]["requestId"])
	}
}

// ─── The previous() path ──────────────────────────────────────────────────────

func testCallback(t *testing.T, base string, client Doer) *Callback {
	t.Helper()
	callback, err := NewCallback(
		func(*http.Request) string { return base },
		func(*http.Request) string { return "public" },
		"service-role-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	return callback
}

// An update carries a path the hook can call to read the rows as they stand. An
// insert does not: there are no prior rows, and the generated types omit
// `previous` entirely rather than offering a call that returns nothing.
func TestThePreviousPathIsOfferedOnlyWhereThereIsSomethingToRead(t *testing.T) {
	callback := testCallback(t, "http://postgrest", nil)

	for name, tc := range map[string]struct {
		method  string
		event   string
		body    string
		offered bool
	}{
		"an update": {http.MethodPatch, EventBeforeChange, `{"title":"new"}`, true},
		"a delete":  {http.MethodDelete, EventBeforeDelete, "", true},
		"an insert": {http.MethodPost, EventBeforeChange, `{"title":"new"}`, false},
	} {
		var seen []map[string]any
		fnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var parsed map[string]any
			_ = json.Unmarshal(raw, &parsed)
			seen = append(seen, parsed)
			w.WriteHeader(http.StatusOK)
		}))

		mw := Middleware(Options{
			Dispatcher: NewDispatcher(nil, ""),
			Hooks: func(*http.Request) map[string]TableHooksView {
				return map[string]TableHooksView{"posts": {tc.event: HookConfigEntry{Function: "check"}}}
			},
			ResolveURL: func(*http.Request, string) (string, error) { return fnServer.URL, nil },
			RequestID:  func(*http.Request) string { return "req-1" },
			Callback:   callback,
		})

		req := httptest.NewRequest(tc.method, "/posts?id=eq.1", strings.NewReader(tc.body))
		mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
		})).ServeHTTP(httptest.NewRecorder(), req)
		fnServer.Close()

		if len(seen) != 1 {
			t.Fatalf("%s: payloads = %+v", name, seen)
		}
		path, present := seen[0]["previousPath"]
		if present != tc.offered {
			t.Errorf("%s: previousPath present = %v, want %v", name, present, tc.offered)
		}
		if tc.offered && !strings.HasPrefix(path.(string), PreviousPathPrefix) {
			t.Errorf("%s: previousPath = %v", name, path)
		}
	}
}

// A token the callback did not mint is refused, whichever way it is broken.
func TestThePreviousEndpointRefusesWhatItCannotRead(t *testing.T) {
	callback := testCallback(t, "http://postgrest", nil)
	handler := callback.Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status = %d, want 405", rec.Code)
	}

	notBase64 := "!!!"
	notJSON := base64.RawURLEncoding.EncodeToString([]byte("{"))

	for name, token := range map[string]string{
		"nothing":              "",
		"no separator":         "abcdef",
		"a bad signature":      "abc.def",
		"a payload not base64": notBase64 + "." + callback.sign(notBase64),
		"a payload not JSON":   notJSON + "." + callback.sign(notJSON),
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/"+token, nil))
		if rec.Code == http.StatusOK {
			t.Errorf("%s was accepted", name)
		}
	}
}

// An upstream that will not answer is a bad gateway: the hook asked for rows
// and none can be produced, which is not the same as there being none.
func TestThePreviousEndpointWithAnUnreachableUpstream(t *testing.T) {
	callback := testCallback(t, "http://127.0.0.1:1", &http.Client{Timeout: time.Second})

	// Mounted at PreviousPathPrefix on the outer mux, so the handler sees the
	// token as the whole path.
	token := strings.TrimPrefix(callback.Path(OpUpdate, "posts", "id=eq.1"), PreviousPathPrefix)
	rec := httptest.NewRecorder()
	callback.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/"+token, nil))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (%s)", rec.Code, rec.Body.String())
	}
}

// A path for an insert, or for no table, is no path at all.
func TestThePreviousPathForNothingToRead(t *testing.T) {
	callback := testCallback(t, "http://postgrest", nil)

	if got := callback.Path(OpInsert, "posts", ""); got != "" {
		t.Errorf("an insert offered %q", got)
	}
	if got := callback.Path(OpUpdate, "", "id=eq.1"); got != "" {
		t.Errorf("no table offered %q", got)
	}
	var absent *Callback
	if got := absent.Path(OpUpdate, "posts", ""); got != "" {
		t.Errorf("no callback offered %q", got)
	}
}

// ─── The recorded response ────────────────────────────────────────────────────

// A handler that writes a body without setting a status has answered 200, and
// the after hook has to be told that rather than nothing.
func TestARecordedResponseWithNoExplicitStatus(t *testing.T) {
	r := &recorder{ResponseWriter: httptest.NewRecorder()}

	if _, err := r.Write([]byte("body")); err != nil {
		t.Fatal(err)
	}
	if r.status != http.StatusOK {
		t.Errorf("status = %d, want 200", r.status)
	}
}

// An insert is always presented as an array, so a handler never has to branch
// on whether it received one row or several.

func TestRowsAreAlwaysAnArray(t *testing.T) {
	if got := string(rowsPayload(nil)); got != "[]" {
		t.Errorf("nothing = %q", got)
	}
	if got := string(rowsPayload([]byte("  \n "))); got != "[]" {
		t.Errorf("whitespace = %q", got)
	}
	if got := string(rowsPayload([]byte(`[{"a":1}]`))); got != `[{"a":1}]` {
		t.Errorf("a list = %q", got)
	}
	if got := string(rowsPayload([]byte(`{"a":1}`))); got != `[{"a":1}]` {
		t.Errorf("a single row = %q, want it wrapped", got)
	}
}

// ─── Reading the rows a write is about to change ──────────────────────────────

// The callback reads the affected rows through PostgREST, so what it does when
// PostgREST is missing, refuses, or answers with something else decides whether
// a hook is handed the wrong picture of the row it is validating.
func TestFetchingThePreviousRows(t *testing.T) {
	answering := func(t *testing.T, handler http.HandlerFunc) *httptest.Server {
		t.Helper()
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return server
	}

	rowsFrom := func(t *testing.T, base string) (json.RawMessage, int) {
		t.Helper()
		callback := testCallback(t, base, &http.Client{Timeout: 2 * time.Second})
		token := strings.TrimPrefix(callback.Path(OpUpdate, "posts", "id=eq.1"), PreviousPathPrefix)
		rec := httptest.NewRecorder()
		callback.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/"+token, nil))
		return rec.Body.Bytes(), rec.Code
	}

	// The happy path: PostgREST answers with rows and they come back.
	server := answering(t, func(w http.ResponseWriter, r *http.Request) {
		// The callback reads with the service role, because a hook has to see
		// the rows as stored rather than as the caller could see them.
		if r.Header.Get("Authorization") == "" {
			t.Error("the fetch carried no credential")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1,"title":"before"}]`))
	})
	body, status := rowsFrom(t, server.URL)
	if status != http.StatusOK {
		t.Fatalf("status = %d (%s)", status, body)
	}
	if !strings.Contains(string(body), "before") {
		t.Errorf("body = %s", body)
	}

	// Nothing matched is an empty list, not nothing: a hook iterating the rows
	// must not have to test for absence first.
	empty := answering(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	body, status = rowsFrom(t, empty.URL)
	if status != http.StatusOK || !strings.Contains(string(body), "[]") {
		t.Errorf("no rows: %d %s", status, body)
	}

	// And the ways it can fail.
	refusing := answering(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	notRows := answering(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":"not a list"}`))
	})

	for name, base := range map[string]string{
		"no PostgREST configured":    "",
		"PostgREST refuses":          refusing.URL,
		"an answer that is not rows": notRows.URL,
		"a URL that is not one":      "://nonsense",
	} {
		if _, status := rowsFrom(t, base); status != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502", name, status)
		}
	}
}

// ─── Validators that cannot be reached ────────────────────────────────────────

// A validator whose function URL will not resolve did not run, so the
// unavailability policy decides. Defaulting to allow would let a write the
// schema said to validate arrive unvalidated.
func TestAValidatorWhoseURLWillNotResolve(t *testing.T) {
	for name, tc := range map[string]struct {
		onUnavailable string
		wantUpstream  bool
	}{
		"refusing by default": {"", false},
		"configured to log":   {"log", true},
	} {
		var upstreamCalled bool
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			upstreamCalled = true
			w.WriteHeader(http.StatusCreated)
		})

		mw := Middleware(Options{
			Dispatcher: NewDispatcher(nil, ""),
			Hooks:      func(*http.Request) map[string]TableHooksView { return nil },
			Validators: func(*http.Request) map[string]TableValidatorsView {
				return map[string]TableValidatorsView{
					"posts": {"title": HookConfigEntry{Function: "check", OnUnavailable: tc.onUnavailable}},
				}
			},
			ResolveURL: func(*http.Request, string) (string, error) {
				return "", errors.New("no worker for that function")
			},
			RequestID: func(*http.Request) string { return "req-1" },
		})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"title":"x"}`))
		mw(next).ServeHTTP(rec, req)

		if upstreamCalled != tc.wantUpstream {
			t.Errorf("%s: the write went through = %v, want %v (status %d)",
				name, upstreamCalled, tc.wantUpstream, rec.Code)
		}
	}
}

// A body that fails mid-read leaves nothing to hand the hook, and reporting it
// beats handing over the rows that did arrive.
func TestFetchingRowsFromAnAnswerThatStops(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[{"))
		if hijacker, ok := w.(http.Hijacker); ok {
			if conn, _, err := hijacker.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	}))
	defer server.Close()

	callback := testCallback(t, server.URL, &http.Client{Timeout: 2 * time.Second})
	token := strings.TrimPrefix(callback.Path(OpUpdate, "posts", "id=eq.1"), PreviousPathPrefix)

	rec := httptest.NewRecorder()
	callback.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/"+token, nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (%s)", rec.Code, rec.Body.String())
	}
}

// The branch is unreachable with a real payload, so the seam proves a validator
// that could not be called refuses rather than letting the write past.
func TestAValidatorPayloadThatCannotBeEncoded(t *testing.T) {
	original := marshalPayload
	t.Cleanup(func() { marshalPayload = original })
	marshalPayload = func(any) ([]byte, error) { return nil, errors.New("nope") }

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	for name, tc := range map[string]struct {
		onUnavailable string
		wantUpstream  bool
	}{
		"refusing by default": {"", false},
		"configured to log":   {"log", true},
	} {
		var upstreamCalled bool
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			upstreamCalled = true
			w.WriteHeader(http.StatusCreated)
		})

		mw := Middleware(Options{
			Dispatcher: NewDispatcher(nil, ""),
			Hooks:      func(*http.Request) map[string]TableHooksView { return nil },
			Validators: func(*http.Request) map[string]TableValidatorsView {
				return map[string]TableValidatorsView{
					"posts": {"title": HookConfigEntry{Function: "check", OnUnavailable: tc.onUnavailable}},
				}
			},
			ResolveURL: func(*http.Request, string) (string, error) { return server.URL, nil },
			RequestID:  func(*http.Request) string { return "req-1" },
			Claims:     func(*http.Request) *Claims { return nil },
		})

		rec := httptest.NewRecorder()
		mw(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"title":"x"}`)))

		if upstreamCalled != tc.wantUpstream {
			t.Errorf("%s: the write went through = %v, want %v (status %d)",
				name, upstreamCalled, tc.wantUpstream, rec.Code)
		}
	}
}

// What a caller is told when a field validator says no. The validator's own
// body is preferred when it already names a field, so a handler that wants to
// say something more specific is not overwritten.
func TestWriteFieldRejection(t *testing.T) {
	for name, tc := range map[string]struct {
		outcome    Outcome
		wantStatus int
		wantField  string
		wantMsg    string
	}{
		"a validator that named the field": {
			Outcome{Status: http.StatusConflict, Body: []byte(`{"field":"slug","message":"already taken"}`)},
			http.StatusConflict, "slug", "already taken",
		},
		"a validator that only had a message": {
			Outcome{Body: []byte(`{"message":"too short"}`)},
			http.StatusUnprocessableEntity, "title", "too short",
		},
		"a validator that used error instead": {
			Outcome{Body: []byte(`{"error":"too short"}`)},
			http.StatusUnprocessableEntity, "title", "too short",
		},
		"a validator that said nothing": {
			Outcome{},
			http.StatusUnprocessableEntity, "title", "This value was rejected.",
		},
		"a body nobody can read": {
			Outcome{Body: []byte(`{not json`)},
			http.StatusUnprocessableEntity, "title", "This value was rejected.",
		},
	} {
		rec := httptest.NewRecorder()
		writeFieldRejection(rec, "title", tc.outcome)

		if rec.Code != tc.wantStatus {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, tc.wantStatus)
		}
		var body struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("%s: body is not JSON: %s", name, rec.Body.String())
			continue
		}
		if body.Field != tc.wantField || body.Message != tc.wantMsg {
			t.Errorf("%s: body = %+v, want field %q and message %q", name, body, tc.wantField, tc.wantMsg)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s: content-type = %q", name, got)
		}
	}
}
