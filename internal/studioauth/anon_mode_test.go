package studioauth

import (
	"net/http"
	"testing"

	logtest "github.com/sirupsen/logrus/hooks/test"
)

// Fresh-user test C20: function Invoke as "Anonymous" sent no token, so the
// proxy answered "Authentication required" while a real anon client's call
// succeeded. Studio now sends its session with the anon mode, and the proxy
// forwards the project's anon key as both apikey and bearer, so the request is
// exactly an anon client's, for every role and on every data route.
func TestAnonymousGoesAsTheAnonKey(t *testing.T) {
	for _, role := range KnownStudioRoles() {
		for _, c := range []struct{ method, path string }{
			{invokeMethod(role), "/functions/v1/hello"},
			{http.MethodGet, "/rest/v1/posts"},
			{http.MethodGet, "/graphql/v1"},
		} {
			hook := logtest.NewGlobal()
			next, rec := proxyAs(t, role, ModeAnon, c.method, c.path)
			if !next.reached || next.auth != "Bearer anon-key" || next.apikey != "anon-key" {
				t.Errorf("%s %s: got %d, auth %q, apikey %q", role, c.path, rec.Code, next.auth, next.apikey)
			}
			if got := rec.Header().Get(ActingModeHeader); got != ModeAnon {
				t.Errorf("%s %s: mode %q, want anon", role, c.path, got)
			}
			if recorded(hook, c.path) != nil {
				t.Errorf("%s %s: an anonymous request was recorded as elevated", role, c.path)
			}
		}
	}
}

// invokeMethod is how role invokes a function: a POST for a role that may
// write content, which a viewer may not, so a viewer invokes with a GET.
func invokeMethod(role string) string {
	if perms, _ := PermissionsForRole(role); perms.Write {
		return http.MethodPost
	}
	return http.MethodGet
}

// Anonymous on an administration route stays anonymous, so the route refuses
// it upstream as it refuses any anon client; it is not silently elevated.
func TestAnonymousIsNeverElevated(t *testing.T) {
	next, _ := proxyAs(t, "admin", ModeAnon, http.MethodGet, "/auth/v1/admin/users")
	if !next.reached || next.auth != "Bearer anon-key" || next.apikey != "anon-key" {
		t.Fatalf("admin anon on the auth admin API: auth %q apikey %q", next.auth, next.apikey)
	}
}

// Asking for anonymous does not widen a role: a viewer's write is refused.
func TestAnonymousDoesNotWidenARole(t *testing.T) {
	if next, rec := proxyAs(t, "viewer", ModeAnon, http.MethodPost, "/rest/v1/posts"); next.reached || rec.Code != http.StatusForbidden {
		t.Fatalf("viewer anon write: got %d, reached %v", rec.Code, next.reached)
	}
}

// Acting as an anonymous client needs the project's anon key. Without one the
// request is refused as unavailable rather than sent with the caller's own
// session or with none.
func TestAnonymousWithoutAnAnonKeyIsRefused(t *testing.T) {
	_, viewer := proxyCapturing(t, "viewer", func(c *Config) { c.AnonKey = "" })
	got := viewer(http.MethodGet, "/studio/proxy/rest/v1/posts", http.Header{ActingModeHeader: {ModeAnon}})
	if got.code != http.StatusServiceUnavailable || got.upstream != "" {
		t.Fatalf("got %d at %q, want 503 and no upstream", got.code, got.upstream)
	}
}
