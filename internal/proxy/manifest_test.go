package proxy

import (
	"encoding/json"
	"testing"
)

func TestMergeRouteManifest(t *testing.T) {
	base := &RouteManifest{Schema: "public", PostgRESTURL: "http://old:3000", RealtimeEnabled: false}
	overlay := &RouteManifest{
		Schema:           "app",
		PostgRESTURL:     "http://new:3000",
		RealtimeEnabled:  true,
		FunctionsEnabled: true,
	}
	MergeRouteManifest(base, overlay)
	if base.Schema != "app" || base.PostgRESTURL != "http://new:3000" || !base.RealtimeEnabled || !base.FunctionsEnabled {
		t.Fatalf("unexpected merge: %#v", base)
	}
}

func TestMergeRouteManifest_corsOrigins(t *testing.T) {
	base := &RouteManifest{Schema: "public", CorsAllowedOrigins: []string{"https://a.example"}}
	overlay := &RouteManifest{CorsAllowedOrigins: []string{"https://b.example"}}
	MergeRouteManifest(base, overlay)
	if len(base.CorsAllowedOrigins) != 1 || base.CorsAllowedOrigins[0] != "https://b.example" {
		t.Fatalf("CorsAllowedOrigins merge: %#v", base.CorsAllowedOrigins)
	}
}

func TestMergeRouteManifest_staticCache(t *testing.T) {
	base := &RouteManifest{Schema: "public", StaticCacheHTML: "no-cache"}
	overlay := &RouteManifest{
		StaticCacheHTML: "max-age=60",
		StaticCachePrefixes: map[string]string{
			"/blog/": "public, max-age=120",
		},
	}
	MergeRouteManifest(base, overlay)
	if base.StaticCacheHTML != "max-age=60" {
		t.Fatalf("StaticCacheHTML: %q", base.StaticCacheHTML)
	}
	if base.StaticCachePrefixes["/blog/"] != "public, max-age=120" {
		t.Fatalf("StaticCachePrefixes: %#v", base.StaticCachePrefixes)
	}
}

func TestMergeRouteManifest_viteDevURL(t *testing.T) {
	base := &RouteManifest{Schema: "public", ViteDevURL: "http://old:5173"}
	overlay := &RouteManifest{ViteDevURL: "http://new:5173"}
	MergeRouteManifest(base, overlay)
	if base.ViteDevURL != "http://new:5173" {
		t.Fatalf("ViteDevURL = %q", base.ViteDevURL)
	}
}

func TestMergeRouteManifest_realtimeURL(t *testing.T) {
	base := &RouteManifest{Schema: "public", RealtimeURL: "http://old:4000"}
	overlay := &RouteManifest{RealtimeURL: "http://new:4000"}
	MergeRouteManifest(base, overlay)
	if base.RealtimeURL != "http://new:4000" {
		t.Fatalf("RealtimeURL = %q", base.RealtimeURL)
	}
}

func TestParseRouteManifestJSON(t *testing.T) {
	raw := []byte(`{"schema":"x","postgrest_url":"http://pg:3000","realtime_enabled":true}`)
	m, err := ParseRouteManifestJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Schema != "x" || m.PostgRESTURL != "http://pg:3000" || !m.RealtimeEnabled {
		t.Fatalf("parse: %#v", m)
	}
}

// The manifest is the seam between two repositories: the CLI writes it, this reads it. A key the CLI
// writes and this ignores would be a validator declared in a schema and enforced by nobody, which is
// the failure the whole feature exists to prevent, one layer up.
func TestValidatorsSurviveTheManifestRoundTrip(t *testing.T) {
	// Exactly what `supatype push` writes, including `onUnavailable` written explicitly.
	raw := []byte(`{
	  "validators": {
	    "products": {
	      "setup_items": { "function": "check-items", "timeout": 2000, "onUnavailable": "reject" }
	    }
	  }
	}`)

	var m RouteManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("the CLI's manifest must parse: %v", err)
	}

	entry, ok := m.Validators["products"]["setup_items"]
	if !ok {
		t.Fatal("the validator map must survive parsing, or no field is checked")
	}
	if entry.Function != "check-items" {
		t.Errorf("function = %q", entry.Function)
	}
	if entry.OnUnavailable != "reject" {
		t.Errorf("onUnavailable = %q; an unreachable validator must refuse", entry.OnUnavailable)
	}

	// And it must survive the defensive copy, or a hot reload would drop it.
	copied := CloneRouteManifest(&m)
	if copied.Validators["products"]["setup_items"].Function != "check-items" {
		t.Error("cloning the manifest lost the validator map")
	}
}
