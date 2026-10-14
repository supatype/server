package modes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// SHb: outside dev mode the gateway is the one place that decides CORS. The auth service's own
// handler answers every origin with `*` when nothing is configured, and the allowlist used to let a
// request from an origin it did not recognise fall through to it, so a production server answered
// `evil.example` with `Access-Control-Allow-Origin: *`.

// permissiveDownstream answers the way the auth service's CORS handler does.
func permissiveDownstream(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*called = true
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("downstream"))
	})
}

func TestAnUnlistedOriginGetsNoCORSHeadersFromDownstream(t *testing.T) {
	for name, allowed := range map[string][]string{
		"another origin is listed": {"https://app.example"},
		"nothing is listed":        nil,
	} {
		called := false
		req := httptest.NewRequest(http.MethodGet, "/auth/v1/settings", nil)
		req.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		AllowlistCORSMiddleware(func(*http.Request) []string { return allowed }, permissiveDownstream(&called)).ServeHTTP(rec, req)

		if !called || rec.Body.String() != "downstream" {
			t.Errorf("%s: the request should still be served", name)
		}
		for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Methods"} {
			if got := rec.Header().Get(h); got != "" {
				t.Errorf("%s: %s = %q, want none for an origin nobody allowed", name, h, got)
			}
		}
	}
}

func TestAPreflightFromAnUnlistedOriginIsRefused(t *testing.T) {
	called := false
	req := httptest.NewRequest(http.MethodOptions, "/auth/v1/user", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	AllowlistCORSMiddleware(func(*http.Request) []string { return []string{"https://app.example"} }, permissiveDownstream(&called)).ServeHTTP(rec, req)

	if called {
		t.Error("a preflight from an unlisted origin must not reach a handler that would answer it")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin = %q, want none", got)
	}
}

func TestAWildcardIsHonouredOnlyWhenListed(t *testing.T) {
	called := false
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	req.Header.Set("Origin", "https://anyone.example")
	rec := httptest.NewRecorder()
	AllowlistCORSMiddleware(func(*http.Request) []string { return []string{"*"} }, permissiveDownstream(&called)).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("allow-origin = %q, want *", got)
	}
	// A wildcard and credentials together is a combination browsers refuse; the wildcard wins.
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("allow-credentials = %q, want none beside a wildcard", got)
	}
}

// What a service exposes to an allowed origin is kept; only the grant itself is the gateway's.
func TestExposedHeadersSurviveForAnAllowedOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	AllowlistCORSMiddleware(func(*http.Request) []string { return []string{"https://app.example"} },
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Access-Control-Expose-Headers", "Content-Range")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "Content-Range" {
		t.Errorf("expose-headers = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow-credentials = %q, want the service's answer for a listed origin", got)
	}
}
