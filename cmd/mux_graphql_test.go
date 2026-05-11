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

func TestBuildOuterMux_GraphQLProxyInjectsServiceRoleAndForwardsEndUserAuth(t *testing.T) {
	var (
		mu      sync.Mutex
		gotAuth string
		gotUser string
		gotPath string
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotUser = r.Header.Get("X-Supatype-End-User-Authorization")
		gotPath = r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	cfg := &serverconf.ServerConfig{
		Mode:           "dev",
		ServiceRoleKey: "service-role-jwt",
		PostgRESTURL:   upstream.URL,
	}
	manifest := &proxy.RouteManifest{
		Schema:       "public",
		PostgRESTURL: upstream.URL,
		GraphQLURL:   upstream.URL,
	}

	mf := func(*http.Request) *proxy.RouteManifest { return manifest }
	hp := func() outerhealth.ProbeConfig {
		return outerhealth.ProbeConfigFrom(cfg, manifest, "")
	}
	h := buildOuterMux(cfg, mf, hp, http.NotFoundHandler(), nil, "test", nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/graphql/v1", nil)
	req.Header.Set("Authorization", "Bearer end-user-jwt")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	mu.Lock()
	defer mu.Unlock()

	if gotPath != "/graphql/v1" {
		t.Fatalf("expected upstream path /graphql/v1, got %q", gotPath)
	}
	if gotAuth != "Bearer service-role-jwt" {
		t.Fatalf("expected service-role auth on upstream, got %q", gotAuth)
	}
	if gotUser != "Bearer end-user-jwt" {
		t.Fatalf("expected original end-user auth forwarded, got %q", gotUser)
	}
}
