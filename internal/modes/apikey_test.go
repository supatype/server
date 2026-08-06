package modes

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

const testSecret = "super-secret-jwt-token-with-at-least-32-characters-long"

func signKey(t *testing.T, secret, role string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"role": role,
		"iss":  "supatype",
		"iat":  time.Now().Unix(),
	})
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func serve(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestAPIKeyMiddlewareRejectsUnkeyedDataPlaneRequest(t *testing.T) {
	h := APIKeyMiddleware(testSecret, okHandler())
	rec := serve(h, httptest.NewRequest(http.MethodGet, "/rest/v1/posts?limit=1", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a key, got %d", rec.Code)
	}
}

func TestAPIKeyMiddlewareAcceptsValidKeyFromEitherHeader(t *testing.T) {
	h := APIKeyMiddleware(testSecret, okHandler())
	key := signKey(t, testSecret, "anon")

	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	req.Header.Set("apikey", key)
	if rec := serve(h, req); rec.Code != http.StatusOK {
		t.Fatalf("apikey header: expected 200, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	if rec := serve(h, req); rec.Code != http.StatusOK {
		t.Fatalf("bearer token: expected 200, got %d", rec.Code)
	}
}

// A browser cannot set headers on a WebSocket upgrade, so Realtime passes the
// key in the query string.
func TestAPIKeyMiddlewareAcceptsQueryParam(t *testing.T) {
	h := APIKeyMiddleware(testSecret, okHandler())
	key := signKey(t, testSecret, "anon")
	req := httptest.NewRequest(http.MethodGet, "/realtime/v1/websocket?apikey="+key, nil)
	if rec := serve(h, req); rec.Code != http.StatusOK {
		t.Fatalf("query param: expected 200, got %d", rec.Code)
	}
}

func TestAPIKeyMiddlewareRejectsKeyFromAnotherProject(t *testing.T) {
	h := APIKeyMiddleware(testSecret, okHandler())
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	req.Header.Set("apikey", signKey(t, "a-different-projects-secret-value-32chars", "anon"))
	if rec := serve(h, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a foreign key, got %d", rec.Code)
	}
}

func TestAPIKeyMiddlewareRejectsUnexpectedRole(t *testing.T) {
	h := APIKeyMiddleware(testSecret, okHandler())
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	// Correctly signed, but not a PostgREST role.
	req.Header.Set("apikey", signKey(t, testSecret, "editor"))
	if rec := serve(h, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a non-PostgREST role, got %d", rec.Code)
	}
}

// Each exemption is a caller that cannot send a header at all.
func TestAPIKeyMiddlewareExemptions(t *testing.T) {
	h := APIKeyMiddleware(testSecret, okHandler())
	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"cors preflight", http.MethodOptions, "/rest/v1/posts"},
		{"public storage object", http.MethodGet, "/storage/v1/object/public/media/logo.png"},
		{"auth is gated by gotrue itself", http.MethodGet, "/auth/v1/health"},
		{"health probe", http.MethodGet, "/health"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(h, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s should not require a key, got %d", tc.path, rec.Code)
			}
		})
	}
}

// Misconfiguration must not silently disable the gate.
func TestAPIKeyMiddlewareFailsClosedWithoutSecret(t *testing.T) {
	h := APIKeyMiddleware("", okHandler())
	rec := serve(h, httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when no secret is configured, got %d", rec.Code)
	}
}
