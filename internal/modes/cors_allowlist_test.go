package modes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/auth/internal/proxy"
)

func TestParseCSV(t *testing.T) {
	if got := ParseCSV(""); got != nil {
		t.Fatalf("empty: got %#v", got)
	}
	if got := ParseCSV(" https://a.com , ,https://b.com"); len(got) != 2 || got[0] != "https://a.com" || got[1] != "https://b.com" {
		t.Fatalf("ParseCSV: %#v", got)
	}
}

func TestAllowlistCORSMiddleware_preflight(t *testing.T) {
	origins := []string{"https://app.example"}
	h := AllowlistCORSMiddleware(func(*http.Request) []string { return origins }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("next must not run for preflight")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/rest/v1/x", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatalf("ACAO: %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestAllowlistCORSMiddleware_getAddsHeader(t *testing.T) {
	origins := []string{"https://app.example"}
	h := AllowlistCORSMiddleware(func(*http.Request) []string { return origins }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatalf("ACAO: %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestManagedCORSMiddleware_unionManifest(t *testing.T) {
	mf := func(*http.Request) *proxy.RouteManifest {
		return &proxy.RouteManifest{CorsAllowedOrigins: []string{"https://from.manifest"}}
	}
	h := ManagedCORSMiddleware("https://from.env", mf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://from.manifest")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://from.manifest" {
		t.Fatalf("manifest origin: %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Origin", "https://from.env")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Header().Get("Access-Control-Allow-Origin") != "https://from.env" {
		t.Fatalf("env origin: %q", rec2.Header().Get("Access-Control-Allow-Origin"))
	}
}
