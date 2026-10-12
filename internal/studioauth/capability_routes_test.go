package studioauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	jwt "github.com/golang-jwt/jwt/v5"
)

// proxyRule is one row of the Studio proxy's access matrix: who may make one
// request through `/studio/proxy`.
type proxyRule struct {
	method    string
	path      string
	admin     bool
	developer bool
	editor    bool
}

// Content rows, files, realtime and function invocation are the data plane, and
// every role works with content.
var dataPlaneRules = []proxyRule{
	{http.MethodGet, "/rest/v1/posts", true, true, true},
	{http.MethodHead, "/rest/v1/posts", true, true, true},
	{http.MethodPost, "/rest/v1/posts", true, true, true},
	{http.MethodPatch, "/rest/v1/posts?id=eq.1", true, true, true},
	{http.MethodDelete, "/rest/v1/posts?id=eq.1", true, true, true},
	{http.MethodPost, "/rest/v1/rpc/db_stats", true, true, true},
	{http.MethodPost, "/graphql/v1", true, true, true},
	{http.MethodGet, "/storage/v1/object/media/a.png", true, true, true},
	{http.MethodPost, "/storage/v1/object/media/a.png", true, true, true},
	{http.MethodGet, "/realtime/v1/websocket", true, true, true},
	{http.MethodPost, "/functions/v1/hello", true, true, true},
}

// The routes that already had a rule of their own.
var namedRules = []proxyRule{
	{http.MethodPost, "/sql", true, false, false},
	{http.MethodGet, "/admin/studio-members", true, true, false},
	{http.MethodPatch, "/admin/studio-members/abc", true, false, false},
	{http.MethodDelete, "/admin/studio-members/abc", true, false, false},
	{http.MethodGet, "/auth/v1/admin/users", true, false, false},
	{http.MethodPost, "/auth/v1/admin/users", true, false, false},
}

// The self-host control plane. It runs as the service role: it pushes schema,
// deploys and deletes functions, and its reads include function secrets, so a
// read is as privileged as a write.
var platformRules = []proxyRule{
	{http.MethodGet, "/platform/v1/projects/ref/status", true, false, false},
	{http.MethodGet, "/platform/v1/projects/ref/schema/migrations", true, false, false},
	{http.MethodPost, "/platform/v1/projects/ref/schema/push", true, false, false},
	{http.MethodPost, "/platform/v1/projects/ref/schema/rollback", true, false, false},
	{http.MethodPost, "/platform/v1/projects/ref/functions/deploy", true, false, false},
	{http.MethodDelete, "/platform/v1/projects/ref/functions/hello", true, false, false},
	{http.MethodGet, "/platform/v1/projects/ref/functions/env", true, false, false},
	{http.MethodPost, "/platform/v1/projects/ref/functions/env", true, false, false},
	{http.MethodOptions, "/platform/v1/projects/ref/functions/env", true, false, false},
}

// The Realtime Inspector's control-plane routes: the one narrow exception to
// "the control plane is admin only". Developers debug realtime, so they may
// read channels, connections and the stream, and send a test broadcast; every
// other platform path, realtime settings included, stays admin only.
var realtimeInspectorRules = []proxyRule{
	{http.MethodGet, "/platform/v1/projects/ref/realtime/channels", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/connections", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/stream", true, true, false},
	{http.MethodPost, "/platform/v1/projects/ref/realtime/broadcast", true, true, false},
	{http.MethodGet, "/platform/v1/project", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/settings", true, false, false},
	{http.MethodPatch, "/platform/v1/projects/ref/realtime/settings", true, false, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/metrics", true, false, false},
	{http.MethodPost, "/platform/v1/projects/ref/realtime/channels", true, false, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/broadcast", true, false, false},
	{http.MethodPost, "/platform/v1/project", true, false, false},
	// Matched on whole segments: a tail that climbs out of the inspector's
	// routes is judged as the platform path it is.
	{http.MethodGet, "/platform/v1/projects/ref/realtime/channels/../../schema/migrations", true, false, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/channels/", true, false, false},
	{http.MethodGet, "/platform/v1/projects/../realtime/channels", true, false, false},
	{http.MethodGet, "/platform/v1/projects/%2e%2e/realtime/channels", true, false, false},
	{http.MethodGet, "/platform/v1/projects/ref/x/realtime/channels", true, false, false},
}

// Everything else behind the gateway is administration that only the service
// role can do, and a route added later must land here rather than fall through
// to "may write content rows".
var serviceRoleRules = []proxyRule{
	{http.MethodGet, "/functions/v1/admin/list", true, false, false},
	{http.MethodGet, "/functions/v1/admin/env", true, false, false},
	{http.MethodPost, "/functions/v1/admin/env", true, false, false},
	{http.MethodGet, "/admin/v1/config/rest", true, false, false},
	{http.MethodPatch, "/admin/v1/config/rest", true, false, false},
	{http.MethodPost, "/admin/v1/database/credentials/first-view", true, false, false},
	{http.MethodPost, "/internal/v0hooks/send-email", true, false, false},
	{http.MethodGet, "/some/route/added/later", true, false, false},
	{http.MethodGet, "/", true, false, false},
	{http.MethodGet, "", true, false, false},
	// A prefix only counts on a segment boundary.
	{http.MethodGet, "/rest/v1x", true, false, false},
	{http.MethodPost, "/sqlx", true, false, false},
	{http.MethodGet, "/functions/v1admin/env", true, false, false},
	// The router does not clean paths, so neither does the rule: this one is
	// routed to the control plane, whatever its tail says.
	{http.MethodGet, "/platform/v1/../rest/v1/posts", true, false, false},
	{http.MethodGet, "//platform/v1/projects/ref/functions/env", true, false, false},
}

func TestAllowsRequestMatrix(t *testing.T) {
	groups := map[string][]proxyRule{
		"data plane":   dataPlaneRules,
		"named":        namedRules,
		"platform":     platformRules,
		"service role": serviceRoleRules,
		"inspector":    realtimeInspectorRules,
	}
	for group, rules := range groups {
		for _, rule := range rules {
			for role, want := range map[string]bool{
				RoleAdmin: rule.admin, "developer": rule.developer, "editor": rule.editor,
			} {
				perms, _ := PermissionsForRole(role)
				if got := AllowsRequest(perms, rule.method, rule.path); got != want {
					t.Errorf("%s: %s %s %q: got %v, want %v", group, role, rule.method, rule.path, got, want)
				}
			}
		}
	}
}

// A role this deployment does not know carries no capability, and must reach
// nothing at all.
func TestAllowsRequestUnknownRoleReachesNothing(t *testing.T) {
	var none StudioPermissions
	for _, rules := range [][]proxyRule{dataPlaneRules, namedRules, platformRules, serviceRoleRules, realtimeInspectorRules} {
		for _, rule := range rules {
			if AllowsRequest(none, rule.method, rule.path) {
				t.Errorf("no capability must not reach %s %q", rule.method, rule.path)
			}
		}
	}
}

// proxyThroughRouter mounts the proxy the way the gateway does: under
// `/studio/proxy` with the prefix stripped, forwarding into the router it is
// mounted on. It reports where each request landed.
func proxyThroughRouter(t *testing.T, role string) func(method, target string) (int, string) {
	t.Helper()
	token := signClaims(jwt.MapClaims{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()})
	cfg := Config{
		JWTSecret:      testSecret,
		ServiceRoleKey: "service-role-key",
		AdminRoles:     DefaultAdminRoles,
		StudioRole:     func(string) (string, bool) { return role, true },
	}

	landed := ""
	mark := func(name string) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { landed = name })
	}
	r := chi.NewRouter()
	r.Mount("/rest/v1", mark("rest"))
	r.Mount("/platform/v1", mark("platform"))
	r.Mount("/studio/proxy", http.StripPrefix("/studio/proxy", ProxyHandler(r, cfg)))
	r.Mount("/", mark("app"))

	return func(method, target string) (int, string) {
		landed = ""
		req := httptest.NewRequest(method, target, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, landed
	}
}

func TestProxyRefusesPlatformToNonAdmins(t *testing.T) {
	for _, role := range []string{"developer", "editor"} {
		call := proxyThroughRouter(t, role)
		for _, rule := range platformRules {
			code, landed := call(rule.method, "/studio/proxy"+rule.path)
			if code != http.StatusForbidden || landed != "" {
				t.Errorf("%s %s %s: got %d, landed at %q; want 403 and no upstream",
					role, rule.method, rule.path, code, landed)
			}
		}
		if code, landed := call(http.MethodPost, "/studio/proxy/rest/v1/posts"); code != http.StatusOK || landed != "rest" {
			t.Errorf("%s must still write content rows, got %d at %q", role, code, landed)
		}
	}

	admin := proxyThroughRouter(t, RoleAdmin)
	for _, rule := range platformRules {
		if code, landed := admin(rule.method, "/studio/proxy"+rule.path); code != http.StatusOK || landed != "platform" {
			t.Errorf("admin %s %s: got %d at %q, want the control plane", rule.method, rule.path, code, landed)
		}
	}
}

// The rule must judge the path the router will route, not a decoded copy of it.
// Here the decoded path reads as content rows while the router, which matches
// the escaped form, sends the request to the catch-all.
func TestProxyJudgesTheRoutedPath(t *testing.T) {
	call := proxyThroughRouter(t, "editor")
	code, landed := call(http.MethodGet, "/studio/proxy/%72est/v1/posts")
	if code != http.StatusForbidden || landed != "" {
		t.Fatalf("an escaped prefix must be judged as routed, got %d at %q", code, landed)
	}
}
