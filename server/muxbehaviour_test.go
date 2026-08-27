package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/serverconf"
)

// This is the behaviour lock that matters most.
//
// The route table says a pattern is mounted. This says the request reaches the
// right upstream, at the right path, with the right headers injected, and that
// the per-mode middleware stack still refuses what it is supposed to refuse.
// buildOuterMux composes that stack by nesting constructors; the refactor turns
// it into an ordered list of values. These assertions are what make that a
// refactor rather than a rewrite.

const (
	testJWTSecret  = "behaviour-lock-jwt-secret"
	testHMACSecret = "behaviour-lock-hmac-secret"
	testTenant     = "proj-abc"
)

// hit records that a fake upstream received a request.
type hit struct {
	upstream string
	method   string
	path     string
	query    string
	header   http.Header
}

// rig is the outer mux wired to fake upstreams, one per service.
type rig struct {
	handler http.Handler
	mu      sync.Mutex
	hits    []hit
}

func (r *rig) record(h hit) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hits = append(r.hits, h)
}

func (r *rig) lastHit() (hit, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.hits) == 0 {
		return hit{}, false
	}
	return r.hits[len(r.hits)-1], true
}

// rigOption adjusts the configuration a rig is built with.
type rigOption func(*serverconf.ServerConfig)

// withCORSOrigins sets the managed-mode CORS allowlist. It matters because the
// managed CORS layer only answers a preflight for an origin it recognises;
// otherwise it passes the request down to the gates below.
func withCORSOrigins(origins string) rigOption {
	return func(cfg *serverconf.ServerConfig) { cfg.CorsAllowOrigins = origins }
}

// newRig builds the mux for a mode with every service pointed at a fake that
// records what it saw.
func newRig(t *testing.T, mode string, opts ...rigOption) *rig {
	t.Helper()
	clearAmbientEnv(t)
	r := &rig{}

	fake := func(name string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.record(hit{
				upstream: name,
				method:   req.Method,
				path:     req.URL.Path,
				query:    req.URL.RawQuery,
				header:   req.Header.Clone(),
			})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"upstream":"` + name + `"}`))
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	postgrest := fake("postgrest")
	graphql := fake("graphql")
	storage := fake("storage")
	functions := fake("functions")
	realtime := fake("realtime")

	// The auth handler is mounted in-process, not proxied, so it is a plain
	// handler rather than a server.
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.record(hit{upstream: "auth", method: req.Method, path: req.URL.Path, header: req.Header.Clone()})
		w.WriteHeader(http.StatusOK)
	})

	cfg := &serverconf.ServerConfig{
		Mode:               mode,
		PostgRESTURL:       postgrest.URL,
		GraphQLURL:         graphql.URL,
		StorageURL:         storage.URL,
		FunctionsWorkerURL: functions.URL,
		RealtimeURL:        realtime.URL,
		JWTSecret:          testJWTSecret,
		ServiceRoleKey:     "service-role-key",
		TenantHMACSecret:   testHMACSecret,
		ManagedProjectRef:  testTenant,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	manifest := &proxy.RouteManifest{
		Schema:           "public",
		FunctionsEnabled: true,
		RealtimeEnabled:  true,
	}

	r.handler = buildOuterMux(
		cfg,
		func(*http.Request) *proxy.RouteManifest { return manifest },
		func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") },
		authHandler,
		nil,
		"behaviour-test",
		nil,
		nil,
	)
	return r
}

// do issues a request and returns the recorder.
func (r *rig) do(method, target string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	return rec
}

// TestRoutingReachesTheRightUpstream is the core of the lock: for each public
// path, which service answers and at what path.
func TestRoutingReachesTheRightUpstream(t *testing.T) {
	cases := []struct {
		name         string
		method       string
		target       string
		wantStatus   int
		wantUpstream string
		wantPath     string
	}{
		{
			// StripPrefix: the auth service must not see its own mount prefix.
			name: "auth strips its prefix", method: http.MethodGet, target: "/auth/v1/health",
			wantStatus: http.StatusOK, wantUpstream: "auth", wantPath: "/health",
		},
		{
			name: "rest reaches postgrest", method: http.MethodGet, target: "/rest/v1/posts",
			wantStatus: http.StatusOK, wantUpstream: "postgrest", wantPath: "/posts",
		},
		{
			// The GraphQL mount is PostgREST RPC underneath, so the path is
			// rewritten rather than forwarded.
			name: "graphql becomes an rpc call", method: http.MethodPost, target: "/graphql/v1/",
			wantStatus: http.StatusOK, wantUpstream: "graphql", wantPath: "/rpc/graphql",
		},
		{
			name: "storage proxies", method: http.MethodGet, target: "/storage/v1/object/bucket/key",
			wantStatus: http.StatusOK, wantUpstream: "storage", wantPath: "/object/bucket/key",
		},
		{
			name: "functions reach the worker", method: http.MethodPost, target: "/functions/v1/echo",
			wantStatus: http.StatusOK, wantUpstream: "functions", wantPath: "/echo",
		},
		{
			name: "realtime reaches the service", method: http.MethodGet, target: "/realtime/v1/health",
			wantStatus: http.StatusOK, wantUpstream: "realtime", wantPath: "/health",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, "dev")
			rec := r.do(c.method, c.target, nil)
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			got, ok := r.lastHit()
			if !ok {
				t.Fatalf("no upstream was reached")
			}
			if got.upstream != c.wantUpstream {
				t.Errorf("upstream = %q, want %q", got.upstream, c.wantUpstream)
			}
			if got.path != c.wantPath {
				t.Errorf("upstream path = %q, want %q", got.path, c.wantPath)
			}
		})
	}
}

// TestRestInjectsSchemaHeader locks the header PostgREST needs to select the
// project's schema. Losing it silently serves the wrong schema.
func TestRestInjectsSchemaHeader(t *testing.T) {
	r := newRig(t, "dev")
	if rec := r.do(http.MethodGet, "/rest/v1/posts", nil); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, _ := r.lastHit()
	if schema := got.header.Get("X-Pg-Schema"); schema != "public" {
		t.Errorf("X-Pg-Schema = %q, want %q", schema, "public")
	}
}

// TestGraphQLUsesServiceRoleAndForwardsEndUserAuth locks the two-token shape of
// the GraphQL mount: it authenticates upstream as the service role, and passes
// the caller's own token along in a separate header rather than dropping it.
func TestGraphQLUsesServiceRoleAndForwardsEndUserAuth(t *testing.T) {
	r := newRig(t, "dev")
	rec := r.do(http.MethodPost, "/graphql/v1/", func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer end-user-token")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	got, _ := r.lastHit()
	if auth := got.header.Get("Authorization"); auth != "Bearer service-role-key" {
		t.Errorf("Authorization = %q, want the service role key", auth)
	}
	if fwd := got.header.Get("X-Supatype-End-User-Authorization"); fwd != "Bearer end-user-token" {
		t.Errorf("X-Supatype-End-User-Authorization = %q, want the caller's token", fwd)
	}
	if profile := got.header.Get("Content-Profile"); profile != "graphql_public" {
		t.Errorf("Content-Profile = %q, want graphql_public", profile)
	}
}

// TestHooksRouteIsNotPubliclyInvocable locks a security rule, not a convenience.
// Hooks live under hooks/ on the same worker; a caller holding only the anon key
// must not be able to invoke one directly with a payload of their choosing.
func TestHooksRouteIsNotPubliclyInvocable(t *testing.T) {
	r := newRig(t, "dev")
	rec := r.do(http.MethodPost, "/functions/v1/hooks/moderate", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a direct hook invocation", rec.Code)
	}
	if got, ok := r.lastHit(); ok {
		t.Errorf("the request reached %s; it must not leave the gateway", got.upstream)
	}
}

// TestHealthProbesUpstreamsAndStaysOK records what /health actually does today:
// it fans out to every configured upstream, with a 2s timeout, and reports 200
// regardless of what comes back. Only /health/ready turns a failed probe into a
// 503.
//
// This is a record, not an endorsement. A liveness endpoint that depends on
// upstreams can restart a healthy process because something downstream is slow.
// The refactor must not change it by accident, which is what this locks; whether
// to change it deliberately is a separate decision.
func TestHealthProbesUpstreamsAndStaysOK(t *testing.T) {
	r := newRig(t, "dev")
	rec := r.do(http.MethodGet, "/health", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", rec.Code)
	}
	if _, ok := r.lastHit(); !ok {
		t.Error("GET /health is expected to probe upstreams today; if that changed, update this lock deliberately")
	}
}

func TestDevModeReflectsOrigin(t *testing.T) {
	r := newRig(t, "dev")
	rec := r.do(http.MethodGet, "/rest/v1/posts", func(req *http.Request) {
		req.Header.Set("Origin", "http://localhost:5173")
	})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("dev mode should reflect the Origin, got %q", got)
	}
}

// TestManagedModeRefusesUnkeyedDataPlane locks the API key gate. Without it an
// unkeyed request runs as the anon role, leaving the whole API reachable by
// anyone who knows the project ref.
func TestManagedModeRefusesUnkeyedDataPlane(t *testing.T) {
	r := newRig(t, "managed")
	rec := r.do(http.MethodGet, "/rest/v1/posts", withTenant)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without an API key", rec.Code)
	}
	if got, ok := r.lastHit(); ok {
		t.Errorf("unkeyed request reached %s", got.upstream)
	}
}

func TestManagedModeAcceptsValidAPIKey(t *testing.T) {
	r := newRig(t, "managed")
	rec := r.do(http.MethodGet, "/rest/v1/posts", func(req *http.Request) {
		withTenant(req)
		req.Header.Set("apikey", signedAPIKey(t, "anon"))
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid key (body %s)", rec.Code, rec.Body.String())
	}
	got, ok := r.lastHit()
	if !ok || got.upstream != "postgrest" {
		t.Errorf("keyed request should reach postgrest, got %+v", got)
	}
}

// TestManagedModeRefusesBadTenantSignature locks the HMAC gate.
func TestManagedModeRefusesBadTenantSignature(t *testing.T) {
	r := newRig(t, "managed")
	rec := r.do(http.MethodGet, "/rest/v1/posts", func(req *http.Request) {
		req.Header.Set("X-Supatype-Tenant", testTenant)
		req.Header.Set("X-Supatype-Tenant-Sig", "deadbeef")
		req.Header.Set("apikey", signedAPIKey(t, "anon"))
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a forged tenant signature", rec.Code)
	}
}

// TestManagedModePreflightIsAnsweredByCORS is the ordering assertion, and the
// subtlety most at risk in the refactor.
//
// The managed stack is CORS outside tenant HMAC outside API key. A browser
// preflight carries neither a tenant signature nor an API key, so it only gets
// through because the CORS layer recognises the Origin and answers the OPTIONS
// itself, before either gate sees it. Nest these the other way round and every
// preflight becomes a 401, which breaks every browser client.
func TestManagedModePreflightIsAnsweredByCORS(t *testing.T) {
	const origin = "https://app.example.com"
	r := newRig(t, "managed", withCORSOrigins(origin))
	rec := r.do(http.MethodOptions, "/rest/v1/posts", func(req *http.Request) {
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "GET")
	})
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("preflight was refused with 401: CORS must be outermost, then tenant HMAC, then API key")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
	if got, ok := r.lastHit(); ok {
		t.Errorf("a preflight must be answered at the gateway, but it reached %s", got.upstream)
	}
}

// TestManagedModePreflightFromUnknownOriginIsRefused is the other half of the
// same mechanism: the CORS layer only short-circuits for an origin on the
// allowlist. Anything else falls through to the tenant gate, and an unsigned
// request is refused there.
func TestManagedModePreflightFromUnknownOriginIsRefused(t *testing.T) {
	r := newRig(t, "managed", withCORSOrigins("https://app.example.com"))
	rec := r.do(http.MethodOptions, "/rest/v1/posts", func(req *http.Request) {
		req.Header.Set("Origin", "https://attacker.example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unsigned preflight from an unlisted origin", rec.Code)
	}
}

// TestManagedModePreflightSkipsTheAPIKeyGate locks the OPTIONS exemption in the
// API key layer, using the request Kong actually forwards: tenant headers
// present and signed, no API key, because a browser cannot set one on a
// preflight.
func TestManagedModePreflightSkipsTheAPIKeyGate(t *testing.T) {
	r := newRig(t, "managed")
	rec := r.do(http.MethodOptions, "/rest/v1/posts", func(req *http.Request) {
		withTenant(req)
		req.Header.Set("Origin", "https://app.example.com")
		req.Header.Set("Access-Control-Request-Method", "GET")
	})
	if rec.Code == http.StatusUnauthorized {
		t.Errorf("a signed preflight without an API key must not be refused, got %d", rec.Code)
	}
}

// TestManagedModeLetsAuthThroughWithoutTenantHeaders locks the documented
// bypass: the auth mount enforces its own credentials, and the control plane
// proxies admin calls through it.
func TestManagedModeLetsAuthThroughWithoutTenantHeaders(t *testing.T) {
	r := newRig(t, "managed")
	rec := r.do(http.MethodGet, "/auth/v1/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got, ok := r.lastHit()
	if !ok || got.upstream != "auth" {
		t.Errorf("should reach the auth handler, got %+v", got)
	}
}

// TestManagedModeExemptsPublicStorage locks an exemption that exists because the
// caller physically cannot send a header: public objects are fetched by
// <img src> and plain links.
func TestManagedModeExemptsPublicStorage(t *testing.T) {
	r := newRig(t, "managed")
	rec := r.do(http.MethodGet, "/storage/v1/object/public/bucket/logo.png", withTenant)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 without an API key (body %s)", rec.Code, rec.Body.String())
	}
	got, ok := r.lastHit()
	if !ok || got.upstream != "storage" {
		t.Errorf("should reach storage, got %+v", got)
	}
}

// TestHealthBypassesTenantVerification locks that a liveness probe, which Kong
// does not sign, still answers in managed mode.
func TestHealthBypassesTenantVerification(t *testing.T) {
	r := newRig(t, "managed")
	if rec := r.do(http.MethodGet, "/health", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /health in managed mode = %d, want 200", rec.Code)
	}
}

// withTenant sets the headers Kong would set, correctly signed.
func withTenant(req *http.Request) {
	req.Header.Set("X-Supatype-Tenant", testTenant)
	req.Header.Set("X-Supatype-Tenant-Sig", tenantSig(testTenant, testHMACSecret))
}

func tenantSig(tenant, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tenant))
	return hex.EncodeToString(mac.Sum(nil))
}

// signedAPIKey mints a project API key: an HS256 token whose top-level role
// claim is a PostgREST role.
func signedAPIKey(t *testing.T, role string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"role": role})
	signed, err := token.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(signed, ".") != 2 {
		t.Fatalf("not a JWT: %q", signed)
	}
	return signed
}
