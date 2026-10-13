package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
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
