package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

// A clone must not share the site pointer's target: the per-tenant cache folds overrides onto a
// clone, and one tenant's pointer must never become another's.
func TestCloneRouteManifestCopiesTheSite(t *testing.T) {
	m := &RouteManifest{Schema: "public", Site: &SiteRef{DeploymentID: "d1", Version: "v1"}}
	c := CloneRouteManifest(m)
	if c.Site == nil || c.Site == m.Site || *c.Site != *m.Site {
		t.Fatalf("clone site = %+v, want an equal copy at another address", c.Site)
	}
	c.Site.DeploymentID = "d2"
	if m.Site.DeploymentID != "d1" {
		t.Fatal("writing the clone's site changed the original")
	}
}

func TestMergeRouteManifestTakesTheOverlaysSite(t *testing.T) {
	base := &RouteManifest{Schema: "public"}
	MergeRouteManifest(base, &RouteManifest{Site: &SiteRef{DeploymentID: "d1"}})
	if base.Site == nil || base.Site.DeploymentID != "d1" {
		t.Fatalf("site = %+v", base.Site)
	}
	MergeRouteManifest(base, &RouteManifest{Schema: "app"})
	if base.Site == nil {
		t.Fatal("an overlay with no site dropped the base's")
	}
}

func TestSiteMaxBytesSurvivesMergeAndClone(t *testing.T) {
	m := &RouteManifest{Schema: "public", Site: &SiteRef{DeploymentID: "d1", MaxBytes: 4096}}
	if got := CloneRouteManifest(m).Site.MaxBytes; got != 4096 {
		t.Fatalf("clone max_bytes = %d", got)
	}
	base := &RouteManifest{Schema: "public"}
	MergeRouteManifest(base, m)
	if base.Site == nil || base.Site.MaxBytes != 4096 {
		t.Fatalf("merge dropped max_bytes: %+v", base.Site)
	}
	raw, err := json.Marshal(m.Site)
	if err != nil || !strings.Contains(string(raw), `"max_bytes":4096`) {
		t.Fatalf("json = %s, %v", raw, err)
	}
}
