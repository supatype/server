package modes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDevMiddleware_CORSReflectsOrigin(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := DevMiddleware(next)

	req := httptest.NewRequest(http.MethodGet, "/rest/v1/foo", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}

func TestDevMiddleware_OPTIONSPreflight(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := DevMiddleware(next)

	req := httptest.NewRequest(http.MethodOptions, "/auth/v1/token", nil)
	req.Header.Set("Origin", "http://127.0.0.1:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusTeapot {
		t.Fatal("OPTIONS should be handled by CORS middleware, not next")
	}
	if rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatal("missing CORS headers on OPTIONS")
	}
}
