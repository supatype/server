package restcache

import (
	"context"
	"net/http"
	"strings"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace"
)

// ServerCacheOffered reports whether keyspace-backed REST caching is enabled for this
// request. Self-host (dev/standalone) always returns true. Managed Cloud free tier
// returns false when tenant:{ref}:config has rest_cache_enabled=false.
//
// The client to pass is the one holding the *tenant configuration* — the platform
// keyspace on a split deployment — not the one holding the cached responses. The
// two are the same server on a single-keyspace deployment, which is why this took
// one argument before and why passing the wrong one now fails quietly: the config
// is absent from the project keyspace, and absent is how a tenant that was never
// published looks.
func ServerCacheOffered(ctx context.Context, cfg *config.Config, tenants keyspace.Client, req *http.Request) bool {
	offered, _ := serverCacheOffer(ctx, cfg, tenants, req)
	return offered
}

// serverCacheOffer is ServerCacheOffered with the reason it said no.
//
// The reason is not decoration. Four different things make a managed pod
// refuse, and only one of them is the free tier — which is the one a screen
// turns into "upgrade to keep these responses". Counting a keyspace that is
// down, or a tenant whose configuration was never published, as the tier would
// bill a product failure to the customer as a missing feature.
func serverCacheOffer(ctx context.Context, cfg *config.Config, tenants keyspace.Client, req *http.Request) (bool, string) {
	if cfg == nil || strings.TrimSpace(cfg.Mode) != "managed" {
		return true, ""
	}
	ref := TenantRef(req, cfg.ManagedProjectRef)
	if ref == "" || tenants == nil {
		return false, ReasonUnavailable
	}
	tc, err := tenants.GetTenantConfig(ctx, ref)
	if err != nil {
		return false, ReasonCacheUnhealthy
	}
	if tc == nil {
		return false, ReasonConfigUnreadable
	}
	if !tc.RestCacheOffered() {
		return false, ReasonTier
	}
	return true, ""
}
