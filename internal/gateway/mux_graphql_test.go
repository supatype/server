package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
)

// graphQLUpstreamSeen is what the fake PostgREST behind the GraphQL mount saw.
type graphQLUpstreamSeen struct {
	auth, apikey, path, contentProfile string
}

// graphQLRig is a mux whose GraphQL mount points at a recording fake.
type graphQLRig struct {
	mu      sync.Mutex
	seen    graphQLUpstreamSeen
	handler http.Handler
}

func newGraphQLRig(t *testing.T) *graphQLRig {
	t.Helper()
	g := &graphQLRig{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.seen = graphQLUpstreamSeen{
			auth:           r.Header.Get("Authorization"),
			apikey:         r.Header.Get("apikey"),
			path:           r.URL.Path,
			contentProfile: r.Header.Get("Content-Profile"),
		}
		g.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(upstream.Close)

	cfg := &config.Config{Mode: "dev", ServiceRoleKey: "service-role-jwt", PostgRESTURL: upstream.URL}
	manifest := &proxy.RouteManifest{Schema: "public", PostgRESTURL: upstream.URL, GraphQLURL: upstream.URL}
	mf := func(*http.Request) *proxy.RouteManifest { return manifest }
	hp := func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") }
	g.handler = buildOuterMux(cfg, mf, hp, http.NotFoundHandler(), nil, "test", nil, nil)
	return g
}

func (g *graphQLRig) snapshot() graphQLUpstreamSeen {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.seen
}

// TestBuildOuterMux_GraphQLForwardsTheCallersOwnAuthorization locks a security
// rule. GraphQL runs in Postgres as whoever PostgREST is told the caller is, so
// the gateway must hand PostgREST the caller's own credentials, exactly as the
// REST mount does. Sending the service role instead runs every query with RLS
// and column grants bypassed, whoever asked.
func TestBuildOuterMux_GraphQLForwardsTheCallersOwnAuthorization(t *testing.T) {
	for name, tc := range map[string]struct{ auth, apikey string }{
		"a signed-in user":      {auth: "Bearer end-user-jwt", apikey: "anon-key"},
		"an anonymous caller":   {auth: "Bearer anon-key", apikey: "anon-key"},
		"no credentials at all": {},
	} {
		g := newGraphQLRig(t)
		req := httptest.NewRequest(http.MethodPost, "/graphql/v1", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		if tc.apikey != "" {
			req.Header.Set("apikey", tc.apikey)
		}
		rr := httptest.NewRecorder()
		g.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: unexpected status: %d", name, rr.Code)
		}
		assertGraphQLUpstream(t, name, g.snapshot(), graphQLUpstreamSeen{
			auth: tc.auth, apikey: tc.apikey, path: "/rpc/graphql", contentProfile: "graphql_public",
		})
	}
}

func assertGraphQLUpstream(t *testing.T, name string, got, want graphQLUpstreamSeen) {
	t.Helper()
	if got != want {
		t.Errorf("%s: upstream saw %+v, want %+v", name, got, want)
	}
	for _, v := range []string{got.auth, got.apikey} {
		if v == "Bearer service-role-jwt" || v == "service-role-jwt" {
			t.Errorf("%s: the gateway sent its own service role key upstream: %q", name, v)
		}
	}
}

// TestBuildOuterMux_GraphQLDisabledAnswersWhy covers a stack whose Postgres has
// no pg_graphql, which is every native `supatype dev` stack. The mount used to
// forward regardless, and the caller got an empty 502 (nothing on the default
// GraphQL upstream) or PostgREST's "could not find the function
// graphql_public.graphql", neither of which says GraphQL is not there. With GraphQL switched off, the gateway answers itself: 501, a stable
// code, a message naming the way out, and nothing sent upstream.
func TestBuildOuterMux_GraphQLDisabledAnswersWhy(t *testing.T) {
	var called atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Store(true)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(upstream.Close)

	cfg := &config.Config{Mode: "dev", PostgRESTURL: upstream.URL, GraphQLDisabled: true}
	manifest := &proxy.RouteManifest{Schema: "public", PostgRESTURL: upstream.URL}
	mf := func(*http.Request) *proxy.RouteManifest { return manifest }
	hp := func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") }
	handler := buildOuterMux(cfg, mf, hp, http.NotFoundHandler(), nil, "test", nil, nil)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(method, "/graphql/v1", nil))
		if rr.Code != http.StatusNotImplemented {
			t.Fatalf("%s: status %d, want 501", method, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type %q, want application/json", method, ct)
		}
		var body struct{ Code, Message string }
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: body is not JSON: %v: %s", method, err, rr.Body.String())
		}
		if body.Code != "graphql_unavailable" {
			t.Errorf("%s: code %q, want graphql_unavailable", method, body.Code)
		}
		if !strings.Contains(body.Message, "Docker provider") {
			t.Errorf("%s: message does not name the way out: %q", method, body.Message)
		}
	}
	if called.Load() {
		t.Error("the request reached PostgREST although GraphQL is switched off")
	}
}
