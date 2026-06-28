package restcache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/serverconf"
)

func TestServerCacheOffered_nonManagedAlwaysTrue(t *testing.T) {
	cfg := &serverconf.ServerConfig{Mode: "standalone"}
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	if !ServerCacheOffered(context.Background(), cfg, nil, req) {
		t.Fatal("expected standalone to offer server cache")
	}
}

func TestServerCacheOffered_managedWithoutValkeyFalse(t *testing.T) {
	cfg := &serverconf.ServerConfig{Mode: "managed", ManagedProjectRef: "abc"}
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	if ServerCacheOffered(context.Background(), cfg, nil, req) {
		t.Fatal("expected managed without valkey to deny server cache")
	}
}
