package proxy

import (
	"encoding/json"
	"testing"
)

// A clone must not share the public values' map: the per-tenant cache folds overrides onto a clone,
// and one tenant's values written into another's would publish them to the wrong site.
func TestCloneRouteManifestCopiesThePublicEnv(t *testing.T) {
	m := &RouteManifest{Schema: "public", PublicEnv: map[string]string{"STRIPE_PK": "pk_live_1"}}
	c := CloneRouteManifest(m)
	if c.PublicEnv["STRIPE_PK"] != "pk_live_1" {
		t.Fatalf("clone public env = %v", c.PublicEnv)
	}
	c.PublicEnv["STRIPE_PK"] = "other"
	if m.PublicEnv["STRIPE_PK"] != "pk_live_1" {
		t.Fatal("writing the clone's public env changed the original")
	}
}

func TestMergeRouteManifestTakesTheOverlaysPublicEnv(t *testing.T) {
	base := &RouteManifest{Schema: "public"}
	MergeRouteManifest(base, &RouteManifest{PublicEnv: map[string]string{"A": "1"}})
	if base.PublicEnv["A"] != "1" {
		t.Fatalf("public env = %v", base.PublicEnv)
	}
	MergeRouteManifest(base, &RouteManifest{Schema: "app"})
	if base.PublicEnv["A"] != "1" {
		t.Fatal("an overlay with no public env dropped the base's")
	}
	MergeRouteManifest(base, &RouteManifest{PublicEnv: map[string]string{"B": "2"}})
	if len(base.PublicEnv) != 1 || base.PublicEnv["B"] != "2" {
		t.Fatalf("an overlay's public env should replace the base's, got %v", base.PublicEnv)
	}
}

func TestPublicEnvIsReadFromTheManifestFile(t *testing.T) {
	var m RouteManifest
	if err := json.Unmarshal([]byte(`{"schema":"public","public_env":{"API_BASE":"https://x.test"}}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.PublicEnv["API_BASE"] != "https://x.test" {
		t.Fatalf("public env = %v", m.PublicEnv)
	}
	raw, err := json.Marshal(&RouteManifest{Schema: "public"})
	if err != nil || string(raw) != `{"schema":"public"}` {
		t.Fatalf("an empty public env must not be written: %s, %v", raw, err)
	}
}
