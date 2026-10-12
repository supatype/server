package studioauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	jwt "github.com/golang-jwt/jwt/v5"
)

// The Realtime Inspector through the Studio proxy.
//
// A developer may reach four realtime routes on the control plane and nothing
// else there. Their own token cannot get past the platform proxy's
// service-role check, so for those routes alone the proxy forwards them
// mediated: the service-role key upstream, and who they are in headers the
// control plane uses to decide what payloads they may see. Those headers are
// only worth anything if a browser cannot write them, so every inbound copy is
// removed before any branch.

// landing is what one proxied request looked like where it arrived.
type landing struct {
	code     int
	upstream string
	header   http.Header
	response http.Header
}

// proxyCapturing is proxyThroughRouter, keeping the headers each request
// arrived upstream with and the headers the proxy answered with.
func proxyCapturing(t *testing.T, role string) (token string, call func(method, target string, extra http.Header) landing) {
	t.Helper()
	token = signClaims(jwt.MapClaims{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()})
	cfg := Config{
		JWTSecret:      testSecret,
		ServiceRoleKey: "service-role-key",
		AnonKey:        "anon-key",
		AdminRoles:     DefaultAdminRoles,
		StudioRole:     func(string) (string, bool) { return role, true },
	}

	var got landing
	mark := func(name string) http.Handler {
		return http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
			got.upstream = name
			got.header = req.Header.Clone()
		})
	}
	r := chi.NewRouter()
	r.Mount("/rest/v1", mark("rest"))
	r.Mount("/platform/v1", mark("platform"))
	r.Mount("/studio/proxy", http.StripPrefix("/studio/proxy", ProxyHandler(r, cfg)))
	r.Mount("/", mark("app"))

	return token, func(method, target string, extra http.Header) landing {
		got = landing{}
		req := httptest.NewRequest(method, target, nil)
		for name, values := range extra {
			req.Header[name] = values
		}
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		got.code = rec.Code
		got.response = rec.Header()
		return got
	}
}

const inspectorChannels = "/studio/proxy/platform/v1/projects/ref/realtime/channels"

func TestProxyAllowsRealtimeInspectorToDevelopers(t *testing.T) {
	_, developer := proxyCapturing(t, "developer")
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/platform/v1/projects/ref/realtime/channels", http.StatusOK},
		{http.MethodGet, "/platform/v1/projects/ref/realtime/connections", http.StatusOK},
		{http.MethodGet, "/platform/v1/projects/ref/realtime/stream", http.StatusOK},
		{http.MethodPost, "/platform/v1/projects/ref/realtime/broadcast", http.StatusOK},
		{http.MethodGet, "/platform/v1/project", http.StatusOK},
		{http.MethodPatch, "/platform/v1/projects/ref/realtime/settings", http.StatusForbidden},
		{http.MethodGet, "/platform/v1/projects/ref/realtime/channels/../../schema/migrations", http.StatusForbidden},
		{http.MethodPost, "/platform/v1/projects/ref/schema/push", http.StatusForbidden},
	} {
		got := developer(c.method, "/studio/proxy"+c.path, nil)
		landedOnPlatform := got.upstream == "platform"
		if got.code != c.want || landedOnPlatform != (c.want == http.StatusOK) {
			t.Errorf("developer %s %s: got %d at %q, want %d", c.method, c.path, got.code, got.upstream, c.want)
		}
	}

	_, editor := proxyCapturing(t, "editor")
	for _, rule := range realtimeInspectorRules {
		got := editor(rule.method, "/studio/proxy"+rule.path, nil)
		if got.code != http.StatusForbidden || got.upstream != "" {
			t.Errorf("editor %s %s: got %d at %q, want 403", rule.method, rule.path, got.code, got.upstream)
		}
	}
}

func TestProxyMediatesTheInspectorForDevelopers(t *testing.T) {
	token, developer := proxyCapturing(t, "developer")
	got := developer(http.MethodGet, inspectorChannels, nil)

	if got.upstream != "platform" {
		t.Fatalf("landed at %q, want the control plane", got.upstream)
	}
	for name, want := range map[string]string{
		"Authorization":                   "Bearer service-role-key",
		"Apikey":                          "service-role-key",
		"X-Supatype-Studio-Actor":         "user-1",
		"X-Supatype-Studio-Role":          "developer",
		"X-Supatype-Studio-Elevated":      "false",
		"X-Supatype-Viewer-Authorization": "Bearer " + token,
	} {
		if v := got.header.Get(name); v != want {
			t.Errorf("upstream %s = %q, want %q", name, v, want)
		}
	}
	if mode := got.response.Get(ActingModeHeader); mode != ModeMediated {
		t.Errorf("acting mode %q, want %q", mode, ModeMediated)
	}
}

func TestProxyStripsForgedStudioIdentity(t *testing.T) {
	forged := http.Header{
		"X-Supatype-Studio-Role":          {"admin"},
		"X-Supatype-Studio-Elevated":      {"true"},
		"X-Supatype-Studio-Actor":         {"someone-else"},
		"X-Supatype-Viewer-Authorization": {"Bearer forged"},
	}

	_, developer := proxyCapturing(t, "developer")
	got := developer(http.MethodGet, inspectorChannels, forged)
	if got.header.Get("X-Supatype-Studio-Role") != "developer" ||
		got.header.Get("X-Supatype-Studio-Elevated") != "false" ||
		got.header.Get("X-Supatype-Studio-Actor") != "user-1" {
		t.Errorf("forged identity reached the control plane: %v", got.header)
	}

	_, editor := proxyCapturing(t, "editor")
	rest := editor(http.MethodGet, "/studio/proxy/rest/v1/posts", forged)
	if rest.upstream != "rest" {
		t.Fatalf("editor rest landed at %q", rest.upstream)
	}
	for name := range forged {
		if v := rest.header.Get(name); v != "" {
			t.Errorf("editor's forged %s reached the data plane as %q", name, v)
		}
	}
}

func TestProxyKeepsDevelopersOwnIdentityOffThePlatform(t *testing.T) {
	token, developer := proxyCapturing(t, "developer")
	got := developer(http.MethodGet, "/studio/proxy/rest/v1/posts", nil)
	if got.upstream != "rest" || got.header.Get("Authorization") != "Bearer "+token {
		t.Fatalf("developer rows must act as themselves, got %q with %q", got.upstream, got.header.Get("Authorization"))
	}
	if got.header.Get("Apikey") != "anon-key" || got.response.Get(ActingModeHeader) != ModeSelf {
		t.Errorf("developer rows: apikey %q mode %q", got.header.Get("Apikey"), got.response.Get(ActingModeHeader))
	}
	if got.header.Get("X-Supatype-Studio-Role") != "" {
		t.Errorf("identity headers are for the control plane only")
	}
}

func TestProxyNamesAdminsToTheControlPlane(t *testing.T) {
	_, admin := proxyCapturing(t, RoleAdmin)
	for _, path := range []string{"/platform/v1/projects/ref/status", "/platform/v1/projects/ref/realtime/stream"} {
		got := admin(http.MethodGet, "/studio/proxy"+path, nil)
		if got.upstream != "platform" || got.header.Get("Authorization") != "Bearer service-role-key" {
			t.Fatalf("admin %s: landed at %q with %q", path, got.upstream, got.header.Get("Authorization"))
		}
		if got.header.Get("X-Supatype-Studio-Elevated") != "true" ||
			got.header.Get("X-Supatype-Studio-Role") != RoleAdmin ||
			got.header.Get("X-Supatype-Studio-Actor") != "user-1" {
			t.Errorf("admin %s: identity %v", path, got.header)
		}
		if got.header.Get("X-Supatype-Viewer-Authorization") != "" {
			t.Errorf("admin %s: an elevated caller has no viewer token to pass on", path)
		}
	}
}
