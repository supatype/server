package valkey

import (
	"context"
	"testing"

	"github.com/supatype/auth/internal/proxy"
)

func TestTenantManifestCache_nilClient(t *testing.T) {
	c := NewTenantManifestCache(nil, 0, func() *proxy.RouteManifest {
		return &proxy.RouteManifest{Schema: "app", PostgRESTURL: "http://pg:3000"}
	})
	m, err := c.Get(context.Background(), "any-ref")
	if err != nil {
		t.Fatal(err)
	}
	if m.Schema != "app" || m.PostgRESTURL != "http://pg:3000" {
		t.Fatalf("%#v", m)
	}
}
