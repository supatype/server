package proxy

import "testing"

func TestMergeRouteManifest(t *testing.T) {
	base := &RouteManifest{Schema: "public", PostgRESTURL: "http://old:3000", RealtimeEnabled: false}
	overlay := &RouteManifest{
		Schema:             "app",
		PostgRESTURL:       "http://new:3000",
		RealtimeEnabled:    true,
		FunctionsEnabled:   true,
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
