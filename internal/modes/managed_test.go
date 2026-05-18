package modes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantMiddleware_healthBypass(t *testing.T) {
	secret := "test-hmac-secret"
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := TenantMiddleware(secret, next)

	for _, path := range []string{"/health", "/health/ready"} {
		called = false
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: expected 200 without tenant headers, got %d", path, rr.Code)
		}
		if !called {
			t.Fatalf("%s: expected next handler to run", path)
		}
	}
}

func TestTenantMiddleware_authV1Bypass(t *testing.T) {
	h := TenantMiddleware("secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/auth/v1/token", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected /auth/v1/* without tenant headers, got %d", rr.Code)
	}
}

func TestTenantMiddleware_requiresTenantHeaders(t *testing.T) {
	h := TenantMiddleware("secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/rest/v1/users", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without tenant headers on /rest/v1/users, got %d", rr.Code)
	}
}
