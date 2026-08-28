package restcache

import (
	"context"
	"net/http"
	"strings"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/valkey"
)

// ServerCacheOffered reports whether Valkey-backed REST caching is enabled for this
// request. Self-host (dev/standalone) always returns true. Managed Cloud free tier
// returns false when tenant:{ref}:config has rest_cache_enabled=false.
func ServerCacheOffered(ctx context.Context, cfg *config.Config, vk *valkey.Client, req *http.Request) bool {
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
