package modes_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/supatype/server/internal/gateway"
	"github.com/supatype/server/internal/modes"
)

// keyExemptRoutes are gateway routes that may be reached without a project API
// key. Every route in the real table must either be gated by the API key
// middleware or appear here with the reason it is not. Adding a route to the
// table without deciding which fails TestEveryGatewayRouteIsGatedOrExempt.
var keyExemptRoutes = map[string]string{
	"/internal/v0hooks/send-email": "auth service hook callback, verified by its own hook signature",
	"/admin/v1":                    "admin API, requires the service role key itself",
	"/studio-config":               "Studio bootstrap, authenticated as a Studio user by the handler",
	"/admin/studio-roles":          "Studio membership, authenticated as a project user with a Studio role",
	"/admin/studio-members":        "Studio membership, authenticated as a project user with a Studio role",
	"/admin/studio-members/*":      "Studio membership, authenticated as a project user with a Studio role",
	"/admin/preview-links":         "preview links, authenticated as a project user with a Studio role",
	"/admin/preview-links/*":       "preview links, authenticated as a project user with a Studio role",
	"/admin/cors-origins":          "CORS origins, Studio role or the control plane's own credentials",
	"/preview-links/resolve":       "the holder of a preview link has no account; the code is the credential",
	"/sql":                         "Studio SQL runner, authenticated as a Studio user by the handler",
	"/metadata":                    "Studio schema queries, authenticated as a Studio user by the handler",
	"/metadata/introspect":         "Studio introspection, authenticated as a Studio user by the handler",
	"/studio-admin/extensions":     "Studio admin operation, authenticated as a Studio user by the handler",
	"/studio-admin/rls":            "Studio admin operation, authenticated as a Studio user by the handler",
	"/studio-admin/policies":       "Studio admin operation, authenticated as a Studio user by the handler",
	"/auth/v1":                     "the auth service enforces its own credentials; sign-in and OAuth callbacks cannot carry a key",
	"/hooks/v1/previous":           "model hook callback, authenticated by its signed token",
	"/admin/v1/functions":          "functions admin, requires the service role key itself",
	"/platform/v1":                 "control plane proxy, authenticated by tenant signature and its own credentials",
	"/_vite/*":                     "dev-mode asset server, only mounted in dev mode",
	"/studio/auth/verify":          "Studio token verification, the token is the credential",
	"/studio/schema":               "Studio bootstrap, authenticated as a Studio user by the handler",
	"/studio/session":              "Studio bootstrap, authenticated as a Studio user by the handler",
	"/studio/proxy":                "Studio proxy, authenticated as a Studio user by the handler",
	"/supatype-env.json":           "public frontend values, public by design",
	"/supatype-env.js":             "public frontend values, public by design",
	"/":                            "the application catch-all, serves the project's own frontend",
}

func gatedWithoutKey(pattern string) bool {
	path := strings.TrimSuffix(pattern, "/*")
	h := modes.APIKeyMiddleware("a-secret-of-at-least-thirty-two-characters", http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	return rec.Code == http.StatusUnauthorized
}

func TestEveryGatewayRouteIsGatedOrExempt(t *testing.T) {
	seen := map[string]bool{}
	for _, route := range gateway.Routes() {
		seen[route.Pattern] = true
		gated := gatedWithoutKey(route.Pattern)
		_, exempt := keyExemptRoutes[route.Pattern]
		switch {
		case route.DataPlane && !gated:
			t.Errorf("data-plane route %s answers without a project API key", route.Pattern)
		case !gated && !exempt:
			t.Errorf("route %s is neither gated by the API key nor on the exemption list", route.Pattern)
		case gated && exempt:
			t.Errorf("route %s is gated but also listed as exempt; drop the exemption", route.Pattern)
		}
	}
	for pattern := range keyExemptRoutes {
		if !seen[pattern] {
			t.Errorf("exemption %s matches no route in the gateway table", pattern)
		}
	}
}
