package outerhealth

import (
	"testing"

	"github.com/supatype/auth/internal/proxy"
	"github.com/supatype/auth/internal/serverconf"
)

func TestProbeConfigFrom(t *testing.T) {
	cfg := &serverconf.ServerConfig{
		PostgRESTURL: "http://env-pg:3000",
		GraphQLURL:   "http://env-gq:3000",
		StorageURL:   "http://env-st:5000",
	}
	m := &proxy.RouteManifest{
		PostgRESTURL:    "http://m-pg:3000",
		GraphQLURL:      "",
		StorageURL:      "http://m-st:5000",
		RealtimeEnabled: true,
	}
	p := ProbeConfigFrom(cfg, m, "http://127.0.0.1:8001")
	if p.PostgRESTURL != "http://m-pg:3000" {
		t.Fatalf("postgrest: %q", p.PostgRESTURL)
	}
	if p.GraphQLURL != "http://env-gq:3000" {
		t.Fatalf("graphql base should prefer cfg when manifest graphql empty: %q", p.GraphQLURL)
	}
	if p.StorageRemoteURL != "http://m-st:5000" {
		t.Fatalf("storage: %q", p.StorageRemoteURL)
	}
	if p.DenoBaseURL != "http://127.0.0.1:8001" {
		t.Fatalf("deno: %q", p.DenoBaseURL)
	}
	if !p.RealtimeEnabled {
		t.Fatal("realtime flag")
	}
}
