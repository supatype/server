package valkey

import (
	"context"
	"sync"
	"time"

	"github.com/supatype/auth/internal/proxy"
	"golang.org/x/sync/singleflight"
)

// TenantManifestCache caches merged route manifests per tenant ref for managed
// mode when SUPATYPE_MANAGED_PROJECT_REF is not set (multi-tenant edge).
type TenantManifestCache struct {
	vk    *Client
	ttl   time.Duration
	base  func() *proxy.RouteManifest
	sf    singleflight.Group
	mu    sync.Mutex
	byRef map[string]tenantManifestEntry
}

type tenantManifestEntry struct {
	m   *proxy.RouteManifest
	exp time.Time
}

// NewTenantManifestCache returns a cache. base must return a fresh clone or
// immutable manifest used as the file layer for each merge. ttl <= 0 defaults
// to 30s.
func NewTenantManifestCache(vk *Client, ttl time.Duration, base func() *proxy.RouteManifest) *TenantManifestCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &TenantManifestCache{
		vk:    vk,
		ttl:   ttl,
		base:  base,
		byRef: make(map[string]tenantManifestEntry),
	}
}

// Get returns the merged manifest for ref, or base() when ref is empty.
func (c *TenantManifestCache) Get(ctx context.Context, ref string) (*proxy.RouteManifest, error) {
	if c == nil || c.vk == nil {
		return proxy.CloneRouteManifest(c.base()), nil
	}
	if ref == "" {
		return proxy.CloneRouteManifest(c.base()), nil
	}

	c.mu.Lock()
	if e, ok := c.byRef[ref]; ok && time.Now().Before(e.exp) {
		out := proxy.CloneRouteManifest(e.m)
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	v, err, _ := c.sf.Do(ref, func() (any, error) {
		base := proxy.CloneRouteManifest(c.base())
		merged, err := LoadMergedManagedManifest(ctx, c.vk, ref, base)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.byRef[ref] = tenantManifestEntry{m: proxy.CloneRouteManifest(merged), exp: time.Now().Add(c.ttl)}
		c.mu.Unlock()
		return merged, nil
	})
	if err != nil {
		return nil, err
	}
	return proxy.CloneRouteManifest(v.(*proxy.RouteManifest)), nil
}

// Flush drops all cached entries (e.g. after local manifest file changes).
func (c *TenantManifestCache) Flush() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.byRef = make(map[string]tenantManifestEntry)
	c.mu.Unlock()
}
