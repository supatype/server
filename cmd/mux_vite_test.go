package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/supatype/auth/internal/outerhealth"
	"github.com/supatype/auth/internal/proxy"
	"github.com/supatype/auth/internal/serverconf"
)

func TestBuildOuterMux_ViteDevProxyStripPrefix(t *testing.T) {
	var (
		mu   sync.Mutex
		path string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path = r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	cfg := &serverconf.ServerConfig{
		Mode:           "dev",
		ServiceRoleKey: "x",
		ViteDevURL:     upstream.URL,
		PostgRESTURL:   upstream.URL,
	}
	manifest := &proxy.RouteManifest{
		Schema:       "public",
		PostgRESTURL: upstream.URL,
		AppMode:      "none",
	}
	mf := func(*http.Request) *proxy.RouteManifest { return manifest }
	hp := func() outerhealth.ProbeConfig {
		return outerhealth.ProbeConfigFrom(cfg, manifest, "")
	}
	h := buildOuterMux(cfg, mf, hp, http.NotFoundHandler(), nil, "test", nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/_vite/@vite/client", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rr.Code, rr.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if path != "/@vite/client" {
		t.Fatalf("upstream path = %q want /@vite/client", path)
	}
}
