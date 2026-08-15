package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/serverconf"
)

// muxWithHooks wires a fake PostgREST and a fake functions worker behind the real outer mux, so the
// assertions below are about the mount and its ordering rather than about the middleware in isolation.
func muxWithHooks(
	t *testing.T,
	hooks map[string]proxy.TableHooks,
	hook http.HandlerFunc,
) (http.Handler, func() bool, func() string) {
	t.Helper()

	var mu sync.Mutex
	var wrote bool
	var upstreamBody string

	postgrest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		wrote = true
		upstreamBody = string(body)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(postgrest.Close)

	worker := httptest.NewServer(http.HandlerFunc(hook))
	t.Cleanup(worker.Close)

	cfg := &serverconf.ServerConfig{
		Mode:               "dev",
		PostgRESTURL:       postgrest.URL,
		FunctionsWorkerURL: worker.URL,
	}
	manifest := &proxy.RouteManifest{
		Schema:             "public",
		PostgRESTURL:       postgrest.URL,
		FunctionsEnabled:   true,
		FunctionsWorkerURL: worker.URL,
		Hooks:              hooks,
	}

	mf := func(*http.Request) *proxy.RouteManifest { return manifest }
	hp := func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") }

	return buildOuterMux(cfg, mf, hp, http.NotFoundHandler(), nil, "test", nil, nil),
		func() bool { mu.Lock(); defer mu.Unlock(); return wrote },
		func() string { mu.Lock(); defer mu.Unlock(); return upstreamBody }
}

func TestRestMountRunsBeforeChangeHook(t *testing.T) {
	h, wrote, upstreamBody := muxWithHooks(t,
		map[string]proxy.TableHooks{
			"posts": {"beforeChange": proxy.HookConfig{Function: "moderate"}},
		},
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Supatype-Hook"); got != "beforeChange" {
				t.Errorf("event header = %q", got)
			}
			// Rewrite the row, to prove the replacement reaches PostgREST through the real chain.
			_, _ = w.Write([]byte(`{"rows":[{"title":"from the hook"}]}`))
		},
	)

	req := httptest.NewRequest(http.MethodPost, "/rest/v1/posts", strings.NewReader(`{"title":"original"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
	if !wrote() {
		t.Fatal("the write never reached PostgREST")
	}
	if got := upstreamBody(); got != `[{"title":"from the hook"}]` {
		t.Fatalf("upstream body = %q, want the hook's replacement", got)
	}
}

func TestRestMountRejectionNeverReachesPostgREST(t *testing.T) {
	h, wrote, _ := muxWithHooks(t,
		map[string]proxy.TableHooks{
			"posts": {"beforeChange": proxy.HookConfig{Function: "moderate"}},
		},
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
		},
	)

	req := httptest.NewRequest(http.MethodPost, "/rest/v1/posts", strings.NewReader(`{"title":"x"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want the hook's 409", rr.Code)
	}
	if wrote() {
		t.Fatal("a rejected write still reached the database")
	}
}

func TestRestMountLeavesReadsAndUnhookedTablesAlone(t *testing.T) {
	// The mount must be free for everything that is not a hooked write: a GET on a hooked table, and
	// any method on a table with no hooks.
	h, _, upstreamBody := muxWithHooks(t,
		map[string]proxy.TableHooks{
			"posts": {"beforeChange": proxy.HookConfig{Function: "moderate"}},
		},
		func(w http.ResponseWriter, _ *http.Request) {
			t.Error("hook called for a request that should not have one")
			w.WriteHeader(http.StatusNoContent)
		},
	)

	get := httptest.NewRequest(http.MethodGet, "/rest/v1/posts?select=*", nil)
	h.ServeHTTP(httptest.NewRecorder(), get)

	post := httptest.NewRequest(http.MethodPost, "/rest/v1/comments", strings.NewReader(`{"body":"hi"}`))
	post.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), post)

	if got := upstreamBody(); got != `{"body":"hi"}` {
		t.Fatalf("upstream body = %q — an unhooked write must pass through untouched", got)
	}
}

func TestRestMountWithNoHooksInManifest(t *testing.T) {
	// The overwhelmingly common deployment: nothing declared, nothing intercepted.
	h, wrote, upstreamBody := muxWithHooks(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("hook called with no hooks in the manifest")
	})

	req := httptest.NewRequest(http.MethodPost, "/rest/v1/posts", strings.NewReader(`{"title":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !wrote() || upstreamBody() != `{"title":"x"}` {
		t.Fatalf("write did not pass through cleanly: wrote=%v body=%q", wrote(), upstreamBody())
	}
}

func TestHooksNamespaceIsNotPubliclyInvocable(t *testing.T) {
	// A hook is procedural — the server calls it around a write. If a caller holding the anon key
	// could POST to it directly, they would choose the payload: a made-up "row about to be deleted",
	// or an afterChange that fires side effects for a write that never happened.
	var called bool
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(worker.Close)

	cfg := &serverconf.ServerConfig{
		Mode:               "dev",
		FunctionsWorkerURL: worker.URL,
	}
	manifest := &proxy.RouteManifest{
		Schema:             "public",
		FunctionsEnabled:   true,
		FunctionsWorkerURL: worker.URL,
	}
	h := buildOuterMux(cfg, func(*http.Request) *proxy.RouteManifest { return manifest },
		func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") },
		http.NotFoundHandler(), nil, "test", nil, nil)

	for _, path := range []string{"/functions/v1/hooks/moderate", "/functions/v1/hooks"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", path, rr.Code)
		}
	}
	if called {
		t.Fatal("a hook was reachable through the public functions path")
	}
}

func TestPublicFunctionsStillInvocable(t *testing.T) {
	// The refusal must be the namespace, not functions in general.
	var gotPath string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(worker.Close)

	cfg := &serverconf.ServerConfig{Mode: "dev", FunctionsWorkerURL: worker.URL}
	manifest := &proxy.RouteManifest{
		Schema:             "public",
		FunctionsEnabled:   true,
		FunctionsWorkerURL: worker.URL,
	}
	h := buildOuterMux(cfg, func(*http.Request) *proxy.RouteManifest { return manifest },
		func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") },
		http.NotFoundHandler(), nil, "test", nil, nil)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/functions/v1/send-email", strings.NewReader(`{}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a public function", rr.Code)
	}
	if !strings.Contains(gotPath, "send-email") {
		t.Fatalf("worker path = %q", gotPath)
	}
}
