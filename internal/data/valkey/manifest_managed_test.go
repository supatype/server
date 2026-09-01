package valkey

import (
	"testing"

	"github.com/supatype/server/internal/proxy"
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

// Cloud has no manifest file, so the hook map has to arrive through tenant config or a project's
// hooks are declared in its schema and never called — every hooked write proceeding unvalidated with
// nothing to show it.
func TestTenantConfigCarriesHooks(t *testing.T) {
	base := &proxy.RouteManifest{Schema: "public"}
	tc := &TenantConfig{
		Hooks: map[string]proxy.TableHooks{
			"posts": {"beforeChange": proxy.HookConfig{Function: "moderate", TimeoutMs: 2000}},
		},
	}

	tc.mergeRoutingInto(base)

	cfg, ok := base.Hooks["posts"]["beforeChange"]
	if !ok {
		t.Fatalf("hooks did not reach the manifest: %+v", base.Hooks)
	}
	if cfg.Function != "moderate" || cfg.TimeoutMs != 2000 {
		t.Fatalf("hook config = %+v, want moderate/2000", cfg)
	}
}

func TestTenantConfigWithoutHooksLeavesTheFileManifestAlone(t *testing.T) {
	// A tenant config that says nothing about hooks must not erase what the file said.
	base := &proxy.RouteManifest{
		Schema: "public",
		Hooks:  map[string]proxy.TableHooks{"posts": {"afterChange": proxy.HookConfig{Function: "index"}}},
	}
	(&TenantConfig{Schema: "public"}).mergeRoutingInto(base)

	if _, ok := base.Hooks["posts"]["afterChange"]; !ok {
		t.Fatalf("an unrelated tenant config dropped the hook map: %+v", base.Hooks)
	}
}

func TestCloneDoesNotShareTheHookMap(t *testing.T) {
	// Per-tenant manifests are clones of the file manifest. A shared map would let one tenant's
	// reload mutate what another tenant's in-flight request is reading.
	original := &proxy.RouteManifest{
		Schema: "public",
		Hooks:  map[string]proxy.TableHooks{"posts": {"beforeChange": proxy.HookConfig{Function: "moderate"}}},
	}
	clone := proxy.CloneRouteManifest(original)
	clone.Hooks["posts"]["beforeChange"] = proxy.HookConfig{Function: "swapped"}

	if original.Hooks["posts"]["beforeChange"].Function != "moderate" {
		t.Fatal("the clone shared the original's hook map")
	}
}

// Every routing field the tenant config can carry has to reach the manifest, or
// a tenant sets something in the control plane and nothing happens.
func TestEveryRoutingFieldIsCarried(t *testing.T) {
	enabled := true
	tc := &TenantConfig{
		PostgRESTURL:            "http://rest",
		GraphQLURL:              "http://gql",
		StorageURL:              "http://store",
		AppMode:                 "proxy",
		AppUpstream:             "http://app",
		ViteDevURL:              "http://vite",
		AppStaticDir:            "/srv/app",
		Schema:                  "app",
		RealtimeEnabled:         &enabled,
		RealtimeURL:             "http://realtime",
		FunctionsEnabled:        &enabled,
		FunctionsWorkerURL:      "http://worker",
		FunctionWorkerURLs:      map[string]string{"fn": "http://fn", "blank": ""},
		StaticCacheHTML:         "no-store",
		StaticCacheHashedAssets: "max-age=60",
		StaticCachePrefixes:     map[string]string{"/docs": "max-age=300"},
	}

	m := &proxy.RouteManifest{}
	tc.mergeRoutingInto(m)

	for name, got := range map[string]string{
		"postgrest":     m.PostgRESTURL,
		"graphql":       m.GraphQLURL,
		"storage":       m.StorageURL,
		"app mode":      m.AppMode,
		"app upstream":  m.AppUpstream,
		"vite":          m.ViteDevURL,
		"static dir":    m.AppStaticDir,
		"schema":        m.Schema,
		"realtime url":  m.RealtimeURL,
		"functions url": m.FunctionsWorkerURL,
		"cache html":    m.StaticCacheHTML,
		"cache assets":  m.StaticCacheHashedAssets,
	} {
		if got == "" {
			t.Errorf("%s was dropped", name)
		}
	}
	if !m.RealtimeEnabled || !m.FunctionsEnabled {
		t.Errorf("a boolean was dropped: %+v", m)
	}
	if m.FunctionWorkerURLs["fn"] != "http://fn" {
		t.Errorf("worker URLs = %v", m.FunctionWorkerURLs)
	}
	// An empty value is not a URL, and writing it would route a function at
	// nothing.
	if _, present := m.FunctionWorkerURLs["blank"]; present {
		t.Error("an empty worker URL was written")
	}
	if m.StaticCachePrefixes["/docs"] != "max-age=300" {
		t.Errorf("cache prefixes = %v", m.StaticCachePrefixes)
	}
}

// Merging into a manifest that has the maps already adds to them rather than
// replacing them.
func TestMergingIntoExistingMaps(t *testing.T) {
	tc := &TenantConfig{
		FunctionWorkerURLs:  map[string]string{"b": "http://b"},
		StaticCachePrefixes: map[string]string{"/api": "max-age=1"},
	}
	m := &proxy.RouteManifest{
		FunctionWorkerURLs:  map[string]string{"a": "http://a"},
		StaticCachePrefixes: map[string]string{"/docs": "max-age=2"},
	}
	tc.mergeRoutingInto(m)

	if m.FunctionWorkerURLs["a"] != "http://a" || m.FunctionWorkerURLs["b"] != "http://b" {
		t.Errorf("worker URLs = %v", m.FunctionWorkerURLs)
	}
	if m.StaticCachePrefixes["/docs"] != "max-age=2" || m.StaticCachePrefixes["/api"] != "max-age=1" {
		t.Errorf("cache prefixes = %v", m.StaticCachePrefixes)
	}
}

// Nothing to merge, or nothing to merge into, is a no-op rather than a panic.
func TestMergingNothing(t *testing.T) {
	(*TenantConfig)(nil).mergeRoutingInto(&proxy.RouteManifest{})
	(&TenantConfig{Schema: "app"}).mergeRoutingInto(nil)
}
