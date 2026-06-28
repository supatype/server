package restcache

import (
	"context"
	"net/http"
	"strings"

	"github.com/supatype/server/internal/serverconf"
	"github.com/supatype/server/internal/valkey"
)

// ServerCacheOffered reports whether Valkey-backed REST caching is enabled for this
// request. Self-host (dev/standalone) always returns true. Managed Cloud free tier
// returns false when tenant:{ref}:config has rest_cache_enabled=false.
func ServerCacheOffered(ctx context.Context, cfg *serverconf.ServerConfig, vk *valkey.Client, req *http.Request) bool {
	if cfg == nil || strings.TrimSpace(cfg.Mode) != "managed" {
		return true
	}
	ref := TenantRef(req, cfg.ManagedProjectRef)
	if ref == "" || vk == nil {
		return false
	}
	tc, err := vk.GetTenantConfig(ctx, ref)
	if err != nil || tc == nil {
		return false
	}
	return tc.RestCacheOffered()
}
