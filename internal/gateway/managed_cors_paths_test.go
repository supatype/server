package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
)

// On Cloud the project's gateway is the one place that decides CORS for its project (Kong carries no
// CORS plugin on tenant routes), and the list it decides from arrives in the route manifest's
// cors_allowed_origins: the project's own origin, schema_config.cors.allowedOrigins and its verified
// custom domains. These run the real router, so a path whose preflight escaped the CORS layer, or a
// service behind it answering a preflight itself, would show here.

const (
	projectOrigin = "https://stz9gysx9v.supatype.io"
	partnerOrigin = "https://www.partner.example"
)

// dataPlanePaths is every data-plane path a browser calls on a project, one per service.
var dataPlanePaths = []string{
	"/functions/v1/contact",
	"/rest/v1/posts",
	"/storage/v1/object/public/avatars/a.png",
	"/auth/v1/token",
	"/graphql/v1",
	"/realtime/v1/websocket",
}

// refusing answers every request 403 with an empty body, as the service behind the gateway answered
// the preflight in the report: a preflight that reaches it gets no CORS headers.
var refusing = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusForbidden)
})

// permissive answers as a service with its own permissive CORS does (the auth service answers `*`):
// a preflight that reached it would be granted.
var permissive = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.WriteHeader(http.StatusNoContent)
})

// manifestGateway is a managed tenant gateway whose only allowed origins are what its manifest lists,
// with every service behind it, auth included, answering as upstream does.
func manifestGateway(t *testing.T, upstream http.Handler, origins ...string) http.Handler {
	t.Helper()
	clearAmbientEnv(t)
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	cfg := &config.Config{
		Mode: "managed", PostgRESTURL: srv.URL, GraphQLURL: srv.URL, StorageURL: srv.URL,
		FunctionsWorkerURL: srv.URL, RealtimeURL: srv.URL,
		JWTSecret: testJWTSecret, ServiceRoleKey: "service-role-key", TenantHMACSecret: testHMACSecret, ManagedProjectRef: testTenant,
	}
	manifest := &proxy.RouteManifest{
		Schema:             "public",
		Services:           proxy.SwitchesOf(map[string]bool{proxy.ServiceFunctions: true, proxy.ServiceRealtime: true}),
		CorsAllowedOrigins: origins,
	}
	return buildOuterMux(cfg,
		func(*http.Request) *proxy.RouteManifest { return manifest },
		func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") },
		upstream, nil, "managed-cors-test", nil, nil)
}

// browserPreflight is what a browser sends before a POST with a JSON body and an API key, through
// Kong, which signs it for the tenant.
func browserPreflight(target, origin string) *http.Request {
	req := httptest.NewRequest(http.MethodOptions, target, nil)
	withTenant(req)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type,apikey")
	return req
}

func TestAPreflightFromAManifestOriginIsAnsweredOnEveryDataPlanePath(t *testing.T) {
	h := manifestGateway(t, refusing, projectOrigin, partnerOrigin)
	for _, path := range dataPlanePaths {
		for _, origin := range []string{projectOrigin, partnerOrigin} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, browserPreflight(path, origin))
			if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != origin {
				t.Errorf("%s from %s: status %d, allow-origin %q; want 204 and the origin",
					path, origin, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
			}
			if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "content-type,apikey" {
				t.Errorf("%s from %s: allow-headers %q", path, origin, got)
			}
		}
	}
}

func TestAPreflightFromAnOriginOffTheManifestIsRefusedWithNoCORSHeaders(t *testing.T) {
	h := manifestGateway(t, permissive, projectOrigin)
	for _, path := range dataPlanePaths {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, browserPreflight(path, partnerOrigin))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want a 403 refusal", path, rec.Code)
		}
		for _, name := range corsHeaderNames {
			if got := rec.Header().Get(name); got != "" {
				t.Errorf("%s: %s = %q for an unlisted origin", path, name, got)
			}
		}
	}
}

// corsHeaderNames are the headers that would grant an origin anything.
var corsHeaderNames = []string{
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Methods",
	"Access-Control-Allow-Headers",
	"Access-Control-Allow-Credentials",
}
