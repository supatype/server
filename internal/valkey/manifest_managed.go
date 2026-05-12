package valkey

import (
	"context"
	"fmt"

	"github.com/supatype/auth/internal/proxy"
)

// RouteManifestKey returns the Valkey key for a full route manifest override
// (same JSON shape as .supatype/manifest.json).
func RouteManifestKey(ref string) string {
	return fmt.Sprintf("tenant:%s:manifest", ref)
}

// LoadMergedManagedManifest builds the effective route manifest for a managed
// pod: start from fileManifest (typically SUPATYPE_MANIFEST_PATH), overlay
// routing fields from tenant:{ref}:config, then overlay tenant:{ref}:manifest
// when present.
func LoadMergedManagedManifest(ctx context.Context, c *Client, ref string, fileManifest *proxy.RouteManifest) (*proxy.RouteManifest, error) {
	out := proxy.CloneRouteManifest(fileManifest)

	tc, err := c.GetTenantConfig(ctx, ref)
	if err != nil {
		return nil, err
	}
	if tc != nil {
		tc.mergeRoutingInto(out)
	}

	raw, err := c.GetBytes(ctx, RouteManifestKey(ref))
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		overlay, err := proxy.ParseRouteManifestJSON(raw)
		if err != nil {
			return nil, err
		}
		proxy.MergeRouteManifest(out, overlay)
	}

	return out, nil
}

func (tc *TenantConfig) mergeRoutingInto(m *proxy.RouteManifest) {
	if tc == nil || m == nil {
		return
	}
	if tc.PostgRESTURL != "" {
		m.PostgRESTURL = tc.PostgRESTURL
	}
	if tc.GraphQLURL != "" {
		m.GraphQLURL = tc.GraphQLURL
	}
	if tc.StorageURL != "" {
		m.StorageURL = tc.StorageURL
	}
	if tc.AppMode != "" {
		m.AppMode = tc.AppMode
	}
	if tc.AppUpstream != "" {
		m.AppUpstream = tc.AppUpstream
	}
	if tc.AppStaticDir != "" {
		m.AppStaticDir = tc.AppStaticDir
	}
	if tc.Schema != "" {
		m.Schema = tc.Schema
	}
	if tc.RealtimeEnabled != nil {
		m.RealtimeEnabled = *tc.RealtimeEnabled
	}
	if tc.FunctionsEnabled != nil {
		m.FunctionsEnabled = *tc.FunctionsEnabled
	}
	if len(tc.CorsAllowedOrigins) > 0 {
		m.CorsAllowedOrigins = append([]string(nil), tc.CorsAllowedOrigins...)
	}
	if tc.StaticCacheHTML != "" {
		m.StaticCacheHTML = tc.StaticCacheHTML
	}
	if tc.StaticCacheHashedAssets != "" {
		m.StaticCacheHashedAssets = tc.StaticCacheHashedAssets
	}
	if len(tc.StaticCachePrefixes) > 0 {
		if m.StaticCachePrefixes == nil {
			m.StaticCachePrefixes = make(map[string]string)
		}
		for k, v := range tc.StaticCachePrefixes {
			m.StaticCachePrefixes[k] = v
		}
	}
}
