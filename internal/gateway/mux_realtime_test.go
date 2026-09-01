package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

func TestRealtimeProxy_DisabledReturns404(t *testing.T) {
	cfg := &config.Config{
		Mode:        "dev",
		RealtimeURL: "http://127.0.0.1:4000",
	}
	manifest := &proxy.RouteManifest{RealtimeEnabled: false}
	mf := func(*http.Request) *proxy.RouteManifest { return manifest }

	h := realtimeInvocationProxy(cfg, mf)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when realtime disabled, got %d", rr.Code)
	}
}

func TestRealtimeProxy_ForwardsHTTPToUpstream(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer upstream.Close()

	cfg := &config.Config{Mode: "dev"}
	manifest := &proxy.RouteManifest{
		RealtimeEnabled: true,
		RealtimeURL:     upstream.URL,
	}
	mf := func(*http.Request) *proxy.RouteManifest { return manifest }

	h := realtimeInvocationProxy(cfg, mf)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	if gotPath != "/health" {
		t.Fatalf("expected upstream /health, got %q", gotPath)
	}
}

func TestResolveRealtimeUpstreamURL_prefersManifest(t *testing.T) {
	cfg := &config.Config{RealtimeURL: "http://env:4000"}
	m := &proxy.RouteManifest{RealtimeURL: "http://manifest:4000"}
	u, err := resolveRealtimeUpstreamURL(cfg, m)
	if err != nil {
		t.Fatal(err)
	}
	if u.String() != "http://manifest:4000" {
		t.Fatalf("got %s", u.String())
	}
}
