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
	// By its path alone, the admin API's old address is a call to the function named admin. The
	// proxy judges it as administration only when the gateway will hand it to the admin API
	// (servesFunctionsAdmin), which depends on the credential it carries.
	{http.MethodGet, "/functions/v1/admin/list", true, true, true},
	{http.MethodPost, "/functions/v1/admin/env", true, true, true},
}

// The routes that already had a rule of their own.
var namedRules = []proxyRule{
	{http.MethodPost, "/sql", true, true, false},
	{http.MethodPost, "/metadata", true, true, false},
	{http.MethodPost, "/metadata/introspect", true, true, true},
	{http.MethodPost, "/studio-admin/extensions", true, true, false},
	{http.MethodPost, "/studio-admin/rls", true, true, false},
	{http.MethodPost, "/studio-admin/policies", true, true, false},
	{http.MethodGet, "/admin/studio-members", true, true, false},
	{http.MethodPatch, "/admin/studio-members/abc", true, false, false},
	{http.MethodDelete, "/admin/studio-members/abc", true, false, false},
	{http.MethodGet, "/auth/v1/admin/users", true, true, false},
	{http.MethodPost, "/auth/v1/admin/users", true, true, false},
	// Listing buckets is where browsing files starts; managing them is not content.
	{http.MethodGet, "/storage/v1/bucket", true, true, true},
	{http.MethodGet, "/storage/v1/bucket/media", true, true, true},
	{http.MethodPost, "/storage/v1/bucket", true, true, false},
	{http.MethodPut, "/storage/v1/bucket/media", true, true, false},
	{http.MethodDelete, "/storage/v1/bucket/media", true, true, false},
	{http.MethodPost, "/storage/v1/bucket/media/empty", true, true, false},
	// Studio membership stays the admin's.
	{http.MethodPost, "/admin/studio-members", true, false, false},
}

// The self-host control plane. It runs as the service role: it pushes schema,
// deploys and deletes functions, and its reads include function secrets, so a
// read is as privileged as a write. Admins and developers administer the project
// (owner decision 2026-10-21); editors never do.
var platformRules = []proxyRule{
	{http.MethodGet, "/platform/v1/projects/ref/status", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/schema/migrations", true, true, false},
	{http.MethodPost, "/platform/v1/projects/ref/schema/push", true, true, false},
	{http.MethodPost, "/platform/v1/projects/ref/schema/rollback", true, true, false},
	{http.MethodPost, "/platform/v1/projects/ref/functions/deploy", true, true, false},
	{http.MethodDelete, "/platform/v1/projects/ref/functions/hello", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/functions/env", true, true, false},
	{http.MethodPost, "/platform/v1/projects/ref/functions/env", true, true, false},
	{http.MethodOptions, "/platform/v1/projects/ref/functions/env", true, true, false},
}

// The Realtime Inspector's control-plane routes. Developers reach them as part
// of the control plane; editors and viewers never do.
var realtimeInspectorRules = []proxyRule{
	{http.MethodGet, "/platform/v1/projects/ref/realtime/channels", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/connections", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/stream", true, true, false},
	{http.MethodPost, "/platform/v1/projects/ref/realtime/broadcast", true, true, false},
	{http.MethodGet, "/platform/v1/project", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/settings", true, true, false},
	{http.MethodPatch, "/platform/v1/projects/ref/realtime/settings", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/metrics", true, true, false},
	{http.MethodPost, "/platform/v1/projects/ref/realtime/channels", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/broadcast", true, true, false},
	{http.MethodPost, "/platform/v1/project", true, true, false},
	// Matched on whole segments: a tail that climbs out of the inspector's
	// routes is judged as the platform path it is.
	{http.MethodGet, "/platform/v1/projects/ref/realtime/channels/../../schema/migrations", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/realtime/channels/", true, true, false},
	{http.MethodGet, "/platform/v1/projects/../realtime/channels", true, true, false},
	{http.MethodGet, "/platform/v1/projects/%2e%2e/realtime/channels", true, true, false},
	{http.MethodGet, "/platform/v1/projects/ref/x/realtime/channels", true, true, false},
}

// Everything else behind the gateway is administration that only the service
// role can do, for admins and developers, and a route added later must land here
// rather than fall through to "may write content rows".
var serviceRoleRules = []proxyRule{
	{http.MethodGet, "/admin/v1/functions/list", true, true, false},
	{http.MethodGet, "/admin/v1/functions/env", true, true, false},
	{http.MethodPost, "/admin/v1/functions/env", true, true, false},
	{http.MethodGet, "/admin/v1/config/rest", true, true, false},
	{http.MethodPatch, "/admin/v1/config/rest", true, true, false},
	{http.MethodPost, "/admin/v1/database/credentials/first-view", true, true, false},
	{http.MethodGet, "/admin/v1/database/connection", true, true, false},
	{http.MethodPost, "/internal/v0hooks/send-email", true, true, false},
	{http.MethodGet, "/some/route/added/later", true, true, false},
	{http.MethodGet, "/", true, true, false},
	{http.MethodGet, "", true, true, false},
	// A prefix only counts on a segment boundary.
	{http.MethodGet, "/rest/v1x", true, true, false},
	{http.MethodPost, "/sqlx", true, true, false},
	{http.MethodGet, "/functions/v1admin/env", true, true, false},
	// The router does not clean paths, so neither does the rule: this one is
	// routed to the control plane, whatever its tail says.
	{http.MethodGet, "/platform/v1/../rest/v1/posts", true, true, false},
	{http.MethodGet, "//platform/v1/projects/ref/functions/env", true, true, false},
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
		Audit:          &fakeAuditor{},
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

// The control plane is for admins and developers (owner decision 2026-10-21);
// an editor reaches none of it, and still writes content rows.
func TestProxyRefusesPlatformToEditors(t *testing.T) {
	editor := proxyThroughRouter(t, "editor")
	for _, rule := range platformRules {
		code, landed := editor(rule.method, "/studio/proxy"+rule.path)
		if code != http.StatusForbidden || landed != "" {
			t.Errorf("editor %s %s: got %d, landed at %q; want 403 and no upstream", rule.method, rule.path, code, landed)
		}
	}
	if code, landed := editor(http.MethodPost, "/studio/proxy/rest/v1/posts"); code != http.StatusOK || landed != "rest" {
		t.Errorf("editor must still write content rows, got %d at %q", code, landed)
	}

	for _, role := range []string{RoleAdmin, "developer"} {
		call := proxyThroughRouter(t, role)
		for _, rule := range platformRules {
			if code, landed := call(rule.method, "/studio/proxy"+rule.path); code != http.StatusOK || landed != "platform" {
				t.Errorf("%s %s %s: got %d at %q, want the control plane", role, rule.method, rule.path, code, landed)
			}
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

// Served on its own rather than under a chi mount, the proxy has no route
// context to read the routed path from, and must judge the escaped form the
// router will match rather than the decoded one.
func TestProxyServedWithoutAMountJudgesTheEscapedPath(t *testing.T) {
	token := signClaims(jwt.MapClaims{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()})
	cfg := Config{
		Audit:          &fakeAuditor{},
		JWTSecret:      testSecret,
		ServiceRoleKey: "service-role-key",
		AdminRoles:     DefaultAdminRoles,
		StudioRole:     func(string) (string, bool) { return "editor", true },
	}
	landed := ""
	inner := chi.NewRouter()
	inner.Mount("/rest/v1", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { landed = "rest" }))
	inner.Mount("/", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { landed = "app" }))
	proxy := ProxyHandler(inner, cfg)

	call := func(target string) int {
		landed = ""
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := call("/rest/v1/posts"); code != http.StatusOK || landed != "rest" {
		t.Fatalf("an editor reading content rows: got %d at %q", code, landed)
	}
	if code := call("/%72est/v1/posts"); code != http.StatusForbidden || landed != "" {
		t.Fatalf("an escaped prefix must be judged as routed, got %d at %q", code, landed)
	}
}

// functionAdminRoutes are the functions admin API's own routes, below either of its addresses.
var functionAdminRoutes = []struct{ method, path string }{
	{http.MethodGet, "/list"},
	{http.MethodGet, "/hello/logs"},
	{http.MethodGet, "/logs/tail"},
	{http.MethodGet, "/env"},
	{http.MethodPost, "/env"},
	{http.MethodDelete, "/env/KEY"},
	{http.MethodGet, "/hello/env"},
	{http.MethodPost, "/hello/env"},
	{http.MethodDelete, "/hello/env/KEY"},
}

// functionAdminAddresses are where the functions admin API is served: its address, and its old one.
var functionAdminAddresses = []string{"/admin/v1/functions", "/functions/v1/admin"}

// A project function named admin is called at /functions/v1/admin, so these are content, not
// administration: the old admin address only answers the admin API's own routes.
var functionNamedAdminPaths = []string{"/functions/v1/admin", "/functions/v1/admin/", "/functions/v1/admin/orders/42"}

// Function administration is not content, at either of its addresses: Read and Write never reach
// one of the admin API's routes, and only Administer does. At the old address that is a request
// the gateway will hand to the admin API (servesFunctionsAdmin).
func TestReadWriteDoesNotReachFunctionAdministration(t *testing.T) {
	content := StudioPermissions{Read: true, Write: true}
	administer := StudioPermissions{Read: true, Write: true, Administer: true}
	for _, address := range functionAdminAddresses {
		servedAsAdmin := address == legacyFunctionsAdminPrefix
		for _, route := range functionAdminRoutes {
			if allowsProxied(content, route.method, address+route.path, servedAsAdmin) {
				t.Errorf("Read/Write reached %s %s, which only Administer may", route.method, address+route.path)
			}
			if !allowsProxied(administer, route.method, address+route.path, servedAsAdmin) {
				t.Errorf("Administer was refused %s %s", route.method, address+route.path)
			}
		}
	}
	// Everything under the new address is administration, a route it does not have included.
	if AllowsRequest(content, http.MethodPost, "/admin/v1/functions/orders/42") {
		t.Error("Read/Write reached POST /admin/v1/functions/orders/42")
	}
}

// A project function named admin follows the rules every function does: Read calls it with a
// read, Write with anything else.
func TestAFunctionNamedAdminIsContent(t *testing.T) {
	read := StudioPermissions{Read: true}
	write := StudioPermissions{Read: true, Write: true}
	for _, path := range functionNamedAdminPaths {
		if !AllowsRequest(read, http.MethodGet, path) {
			t.Errorf("Read was refused GET %s", path)
		}
		if AllowsRequest(read, http.MethodPost, path) {
			t.Errorf("Read reached POST %s, which needs Write", path)
		}
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if !AllowsRequest(write, method, path) {
				t.Errorf("Write was refused %s %s", method, path)
			}
		}
	}
}
