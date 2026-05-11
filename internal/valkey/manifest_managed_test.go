package valkey

import (
	"testing"

	"github.com/supatype/auth/internal/proxy"
)

func TestTenantConfig_mergeRoutingInto(t *testing.T) {
	rt := true
	tc := &TenantConfig{
		PostgRESTURL:    "http://pg:3000",
		GraphQLURL:      "http://pg:3000",
		StorageURL:      "http://st:5000",
		Schema:          "tenant1",
		RealtimeEnabled: &rt,
	}
	m := &proxy.RouteManifest{Schema: "public"}
	tc.mergeRoutingInto(m)
	if m.PostgRESTURL != "http://pg:3000" || m.GraphQLURL != "http://pg:3000" || m.StorageURL != "http://st:5000" || m.Schema != "tenant1" || !m.RealtimeEnabled {
		t.Fatalf("mergeRoutingInto: %#v", m)
	}
}

func TestTenantConfig_mergeRoutingInto_nilPointersNoBoolChange(t *testing.T) {
	tc := &TenantConfig{PostgRESTURL: "http://x:3000"}
	m := &proxy.RouteManifest{Schema: "public", RealtimeEnabled: true}
	tc.mergeRoutingInto(m)
	if !m.RealtimeEnabled {
		t.Fatal("realtime should stay true when tenant omits pointer")
	}
}

func TestTenantConfig_mergeRoutingInto_corsOrigins(t *testing.T) {
	tc := &TenantConfig{CorsAllowedOrigins: []string{"https://a.example", "https://b.example"}}
	m := &proxy.RouteManifest{Schema: "public"}
	tc.mergeRoutingInto(m)
	if len(m.CorsAllowedOrigins) != 2 || m.CorsAllowedOrigins[0] != "https://a.example" {
		t.Fatalf("CorsAllowedOrigins: %#v", m.CorsAllowedOrigins)
	}
}
