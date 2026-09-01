package modelhooks

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// Everything else in this package tests the middleware against a fake upstream. That leaves the one
// join it exists for untested: the request the middleware hands on has to be a request *PostgREST*
// accepts, and the rows previous() returns have to be the rows really stored.
//
// A fake proxy cannot show either. It accepts whatever body it is given, so a substitution that
// PostgREST would reject as malformed looks like a pass, and a `previous()` fetch that builds a URL
// PostgREST answers 400 to looks like an empty result set — a hook validating against "no prior rows"
// then passes every write.
//
// Run with a real stack:
//
//	docker run -d --name pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=app -p 55433:5432 postgres:17-alpine
//	docker run -d --name prst --network … -p 55434:3000 postgrest/postgrest:v12.2.3
//	SUPATYPE_LIVE_POSTGREST=http://127.0.0.1:55434 go test ./internal/modelhooks -run Live
//
// Skipped without that variable, so it never turns CI red for an absent dependency.
func liveStack(t *testing.T) (base string, rest string) {
	t.Helper()
	rest = os.Getenv("SUPATYPE_LIVE_POSTGREST")
	if rest == "" {
		t.Skip("set SUPATYPE_LIVE_POSTGREST to run against a real PostgREST")
	}
	return "", strings.TrimRight(rest, "/")
}

// hookScript is the handler under test for one case, so each subtest supplies its own verdict.
type hookScript func(w http.ResponseWriter, payload payload)

// liveServer mounts the middleware in front of a real PostgREST and returns the caller-facing URL.
func liveServer(t *testing.T, rest string, hooks map[string]TableHooksView, script hookScript) string {
	t.Helper()

	// The hook itself is local: the worker is already proven against the generated adapter elsewhere,
	// and what is unproven here is the server↔PostgREST join.
	var seen payload
	hookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		if err := json.Unmarshal(body, &seen); err != nil {
			http.Error(w, "bad payload", http.StatusInternalServerError)
			return
		}
		script(w, seen)
	}))
	t.Cleanup(hookSrv.Close)

	upstream, err := url.Parse(rest)
	if err != nil {
		t.Fatalf("parsing the PostgREST URL: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.Host = upstream.Host
	}

	callback := NewCallback(
		func(*http.Request) string { return rest },
		func(*http.Request) string { return "public" },
		"", // anon: this fixture grants it directly, and the credential is not what is under test
		nil,
	)

	mw := Middleware(Options{
		Dispatcher: NewDispatcher(nil, "test-secret"),
		Hooks:      func(*http.Request) map[string]TableHooksView { return hooks },
		ResolveURL: func(_ *http.Request, function string) (string, error) {
			return hookSrv.URL + "/" + function, nil
		},
		Claims:   func(*http.Request) *Claims { return nil },
		Callback: callback,
	})

	mux := http.NewServeMux()
	// chi's Mount strips the pattern, which is how production mounts this — the handler reads the token
	// from a mount-relative path, so an unstripped mount makes every token look forged.
	mux.Handle(PreviousPathPrefix,
		http.StripPrefix(strings.TrimSuffix(PreviousPathPrefix, "/"), callback.Handler()))
	// Mounted exactly as production mounts it — under StripPrefix("/rest/v1"), because the matching
	// rules read the table from a /rest/v1-relative path. Mounting it unstripped makes every table look
	// like "rest", so no hook ever fires and every write passes unvalidated: the failure this whole file
	// exists to catch, and it is invisible against a fake upstream.
	mux.Handle("/rest/v1/", http.StripPrefix("/rest/v1", mw(proxy)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// resetFixture empties the table and seeds one row, returning its id.
//
// Per test, because the database outlives the run: a row left by a previous failure would otherwise
// satisfy — or contradict — a later assertion about what is stored, and serial ids make "id=eq.1"
// wrong the moment anything else has inserted.
func resetFixture(t *testing.T, rest string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, rest+"/posts?id=gt.0", nil)
	if err != nil {
		t.Fatalf("building the reset: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("clearing posts: %v", err)
	}
	_ = res.Body.Close()

	seed, err := http.NewRequest(http.MethodPost, rest+"/posts",
		strings.NewReader(`[{"title":"[locked] original","body":"b"}]`))
	if err != nil {
		t.Fatalf("building the seed: %v", err)
	}
	seed.Header.Set("Content-Type", "application/json")
	seed.Header.Set("Prefer", "return=representation")
	created, err := http.DefaultClient.Do(seed)
	if err != nil {
		t.Fatalf("seeding posts: %v", err)
	}
	defer func() { _ = created.Body.Close() }()
	var rows []struct{ ID int }
	if err := json.NewDecoder(created.Body).Decode(&rows); err != nil || len(rows) != 1 {
		t.Fatalf("seeding posts returned %v (%v)", rows, err)
	}
	return rows[0].ID
}

func beforeChangeOn(function string) map[string]TableHooksView {
	return map[string]TableHooksView{
		"posts": {"beforeChange": HookConfigEntry{Function: function, TimeoutMs: 5000}},
	}
}

// titles reads the live table straight from PostgREST, bypassing the middleware, so an assertion about
// what was stored cannot be satisfied by the thing under test.
func titles(t *testing.T, rest, query string) []string {
	t.Helper()
	res, err := http.Get(rest + "/posts?" + query)
	if err != nil {
		t.Fatalf("reading posts: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	var rows []struct{ Title string }
	if err := json.NewDecoder(res.Body).Decode(&rows); err != nil {
		t.Fatalf("decoding posts: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Title)
	}
	return out
}

func send(t *testing.T, method, target, body string, header map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sending the request: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(out)
}

func TestLiveRejectedInsertNeverReachesPostgres(t *testing.T) {
	_, rest := liveStack(t)
	resetFixture(t, rest)
	title := "live-rejected"

	base := liveServer(t, rest, beforeChangeOn("moderate"), func(w http.ResponseWriter, _ payload) {
		http.Error(w, "no", http.StatusUnprocessableEntity)
	})

	status, _ := send(t, http.MethodPost, base+"/rest/v1/posts",
		fmt.Sprintf(`[{"title":%q,"body":"b"}]`, title), nil)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("caller saw %d, want 422", status)
	}
	// The point of the whole feature: a rejected write must not exist.
	if got := titles(t, rest, "title=eq."+title); len(got) != 0 {
		t.Fatalf("the rejected row was stored anyway: %v", got)
	}
}

func TestLiveRewrittenInsertIsWhatPostgresStores(t *testing.T) {
	_, rest := liveStack(t)
	resetFixture(t, rest)
	title := "live-rewritten"

	base := liveServer(t, rest, beforeChangeOn("moderate"), func(w http.ResponseWriter, in payload) {
		var rows []map[string]any
		if err := json.Unmarshal(in.Rows, &rows); err != nil {
			http.Error(w, "unreadable rows", http.StatusInternalServerError)
			return
		}
		rows[0]["title"] = title
		replaced, _ := json.Marshal(rows)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows":` + string(replaced) + `}`))
	})

	// A substituted body has to survive as a *PostgREST-acceptable* request, not merely reach the proxy.
	status, body := send(t, http.MethodPost, base+"/rest/v1/posts", `[{"title":"original","body":"b"}]`,
		map[string]string{"Prefer": "return=representation"})
	if status != http.StatusCreated {
		t.Fatalf("PostgREST answered %d: %s", status, body)
	}
	if got := titles(t, rest, "title=eq."+title); len(got) != 1 {
		t.Fatalf("the rewritten row is not in the table: %v", got)
	}
	if got := titles(t, rest, "title=eq.original"); len(got) != 0 {
		t.Fatalf("the original row was stored too: %v", got)
	}
}

func TestLivePreviousReadsTheRowsAsStored(t *testing.T) {
	_, rest := liveStack(t)
	id := resetFixture(t, rest)

	// Seeded by the fixture as "[locked] original" — a hook that guards on prior state.
	var fetched struct {
		Rows      []map[string]any `json:"rows"`
		Truncated bool             `json:"truncated"`
	}
	// A hook's own failure reaches the caller as a bare 503 by design, so the reason has to be kept
	// here or a broken fixture is indistinguishable from a broken feature.
	var hookErr string
	base := liveServer(t, rest, beforeChangeOn("guard"), func(w http.ResponseWriter, in payload) {
		fail := func(reason string) {
			hookErr = reason
			http.Error(w, reason, http.StatusInternalServerError)
		}
		if in.PreviousPath == "" {
			fail("no previous path was minted")
			return
		}
		// POST, as the generated adapter does: the endpoint refuses anything else, so a GET here would
		// report "no prior rows" for a reason that has nothing to do with the data.
		res, err := http.Post(
			strings.TrimSuffix(callerBase(w), "/")+in.PreviousPath, "application/json", nil)
		if err != nil {
			fail(fmt.Sprintf("calling previous(): %v", err))
			return
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			detail, _ := io.ReadAll(io.LimitReader(res.Body, 512))
			fail(fmt.Sprintf("previous() answered %s: %s", res.Status, detail))
			return
		}
		if err := json.NewDecoder(res.Body).Decode(&fetched); err != nil {
			fail(fmt.Sprintf("previous() body unreadable: %v", err))
			return
		}
		for _, row := range fetched.Rows {
			if title, _ := row["title"].(string); strings.HasPrefix(title, "[locked]") {
				http.Error(w, "that post is locked", http.StatusConflict)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	t.Setenv("SUPATYPE_LIVE_CALLER_BASE", base)

	status, _ := send(t, http.MethodPatch, fmt.Sprintf("%s/rest/v1/posts?id=eq.%d", base, id), `{"title":"changed"}`, nil)
	if status != http.StatusConflict {
		t.Fatalf("caller saw %d, want the hook's 409 (hook reported: %s)", status, hookErr)
	}
	if len(fetched.Rows) != 1 {
		t.Fatalf("previous() returned %d rows through real PostgREST, want 1", len(fetched.Rows))
	}
	if fetched.Truncated {
		t.Fatal("one row was reported as truncated")
	}
	if got := titles(t, rest, fmt.Sprintf("id=eq.%d", id)); len(got) != 1 || got[0] != "[locked] original" {
		t.Fatalf("the guarded row changed: %v", got)
	}
}

func TestLiveAfterChangeSeesWhatPostgresReturned(t *testing.T) {
	_, rest := liveStack(t)
	title := "live-after"

	var after payload
	hooks := map[string]TableHooksView{
		"posts": {"afterChange": HookConfigEntry{Function: "index", TimeoutMs: 5000}},
	}
	done := make(chan struct{})
	base := liveServer(t, rest, hooks, func(w http.ResponseWriter, in payload) {
		after = in
		w.WriteHeader(http.StatusNoContent)
		close(done)
	})

	status, body := send(t, http.MethodPost, base+"/rest/v1/posts",
		fmt.Sprintf(`[{"title":%q,"body":"b"}]`, title),
		map[string]string{"Prefer": "return=representation"})
	if status != http.StatusCreated {
		t.Fatalf("PostgREST answered %d: %s", status, body)
	}
	// Bounded: an after hook that never fires is a failure to report, not a test to hang.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the after hook was never called")
	}

	var rows []map[string]any
	if err := json.Unmarshal(after.Rows, &rows); err != nil {
		t.Fatalf("the after hook got unreadable rows: %v", err)
	}
	if len(rows) != 1 || rows[0]["title"] != title {
		t.Fatalf("after hook saw %v", rows)
	}
	// Serial primary key: the id only exists because PostgREST answered with the stored row, which is
	// the whole reason an after hook is worth having.
	if rows[0]["id"] == nil {
		t.Fatalf("after hook saw no server-assigned id: %v", rows[0])
	}
}

func TestLiveUnhookedWriteIsUntouched(t *testing.T) {
	_, rest := liveStack(t)
	title := "live-unhooked"

	base := liveServer(t, rest, map[string]TableHooksView{
		"other_table": {"beforeChange": HookConfigEntry{Function: "nope", TimeoutMs: 5000}},
	}, func(w http.ResponseWriter, _ payload) {
		http.Error(w, "this hook must never be called", http.StatusTeapot)
	})

	status, body := send(t, http.MethodPost, base+"/rest/v1/posts",
		fmt.Sprintf(`[{"title":%q,"body":"b"}]`, title), nil)
	if status != http.StatusCreated {
		t.Fatalf("PostgREST answered %d: %s", status, body)
	}
	if got := titles(t, rest, "title=eq."+title); len(got) != 1 {
		t.Fatalf("the unhooked write did not land: %v", got)
	}
}

// callerBase is how the fixture's hook reaches back into the server that invoked it. Real hooks read
// their stack URL from the environment; here the server's address is only known once it is listening.
func callerBase(http.ResponseWriter) string {
	return os.Getenv("SUPATYPE_LIVE_CALLER_BASE")
}
