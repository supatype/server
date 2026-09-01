package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/proxy"
)

// A standalone deployment answers every request from the file, and picks up a
// file that changed underneath it.
func TestAStandaloneDeploymentAnswersFromTheFile(t *testing.T) {
	live := newLiveManifests(&proxy.RouteManifest{Schema: "public"})

	req := httptest.NewRequest(http.MethodGet, "/posts", nil)
	if got := live.For(req).Schema; got != "public" {
		t.Errorf("schema = %q", got)
	}

	live.Reapply(&proxy.RouteManifest{Schema: "app"})
	if got := live.For(req).Schema; got != "app" {
		t.Errorf("after reload: schema = %q, want the reloaded file", got)
	}
	if got := live.File().Schema; got != "app" {
		t.Errorf("File() = %q", got)
	}
}

// A tenant header on a deployment that has no per-tenant lookup is ignored
// rather than answered from: a standalone server has one project, and reading
// an attacker-supplied header would be a way to ask for another schema.
func TestATenantHeaderWithoutAPerTenantLookup(t *testing.T) {
	live := newLiveManifests(&proxy.RouteManifest{Schema: "public"})

	req := httptest.NewRequest(http.MethodGet, "/posts", nil)
	req.Header.Set(tenantHeader, "other-project")

	if got := live.For(req).Schema; got != "public" {
		t.Errorf("schema = %q, want the deployment's own", got)
	}
}

// stubTenants is the per-tenant lookup, without a Valkey behind it.
type stubTenants struct {
	byRef   map[string]*proxy.RouteManifest
	err     error
	flushes int
}

func (s *stubTenants) Get(_ context.Context, ref string) (*proxy.RouteManifest, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.byRef[ref], nil
}

func (s *stubTenants) Flush() { s.flushes++ }

// A managed deployment answers each tenant from that tenant's own manifest, and
// falls back to the deployment's when the lookup has nothing or fails. Falling
// back is the point: a Valkey that is briefly away must not take the pod down.
func TestAManagedDeploymentAnswersPerTenant(t *testing.T) {
	tenants := &stubTenants{byRef: map[string]*proxy.RouteManifest{
		"proj-a": {Schema: "tenant_a"},
	}}
	live := newLiveManifests(&proxy.RouteManifest{Schema: "public"})
	live.tenant = tenants

	withTenant := func(ref string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/posts", nil)
		if ref != "" {
			req.Header.Set(tenantHeader, ref)
		}
		return req
	}

	if got := live.For(withTenant("proj-a")).Schema; got != "tenant_a" {
		t.Errorf("the tenant's own: schema = %q", got)
	}
	if got := live.For(withTenant("proj-unknown")).Schema; got != "public" {
		t.Errorf("an unknown tenant: schema = %q, want the deployment's", got)
	}
	if got := live.For(withTenant("")).Schema; got != "public" {
		t.Errorf("no tenant header: schema = %q", got)
	}
	if got := live.For(nil).Schema; got != "public" {
		t.Errorf("no request at all: schema = %q", got)
	}

	tenants.err = errors.New("valkey is away")
	if got := live.For(withTenant("proj-a")).Schema; got != "public" {
		t.Errorf("a lookup that failed: schema = %q, want the deployment's", got)
	}
}

// A file that changed invalidates the per-tenant cache, because each tenant's
// manifest is that file with the tenant's overrides folded onto it.
func TestReloadingTheFileFlushesThePerTenantCache(t *testing.T) {
	tenants := &stubTenants{}
	live := newLiveManifests(&proxy.RouteManifest{Schema: "public"})
	live.tenant = tenants

	live.Reapply(&proxy.RouteManifest{Schema: "app"})
	if tenants.flushes != 1 {
		t.Errorf("flushes = %d, want 1", tenants.flushes)
	}
}

// Base hands the per-tenant lookup a copy. The cache stores what it is given
// and folds a tenant's overrides onto it, so handing over the live value would
// let one tenant's schema become the deployment's.
func TestBaseIsACopy(t *testing.T) {
	live := newLiveManifests(&proxy.RouteManifest{Schema: "public"})

	base := live.Base()
	base.Schema = "tenant_a"

	if got := live.File().Schema; got != "public" {
		t.Errorf("the file manifest was written through: schema = %q", got)
	}
}

// A single-project managed pod merges what the control plane published over the
// file. A merge that fails keeps what was already live rather than dropping
// every tenant back to the file's defaults.
func TestMergingWhatTheControlPlanePublished(t *testing.T) {
	var mergeErr error
	live := newLiveManifests(&proxy.RouteManifest{Schema: "public"})
	live.merge = func(_ context.Context, file *proxy.RouteManifest) (*proxy.RouteManifest, error) {
		if mergeErr != nil {
			return nil, mergeErr
		}
		return &proxy.RouteManifest{Schema: file.Schema + "_merged"}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "/posts", nil)

	live.Reapply(&proxy.RouteManifest{Schema: "app"})
	if got := live.For(req).Schema; got != "app_merged" {
		t.Errorf("schema = %q, want the merged manifest", got)
	}
	// The file itself is still the file, which is what the health probes and the
	// per-tenant base are configured from.
	if got := live.File().Schema; got != "app" {
		t.Errorf("File() = %q, want the file as read", got)
	}

	mergeErr = errors.New("valkey is away")
	live.Reapply(&proxy.RouteManifest{Schema: "later"})
	if got := live.For(req).Schema; got != "app_merged" {
		t.Errorf("after a failed merge: schema = %q, want the previous live manifest", got)
	}
}

// The file watcher's callback is the reload path: a manifest edited under a
// running server has to reach the requests it serves.
func TestTheWatcherCallbackReloads(t *testing.T) {
	live := newLiveManifests(&proxy.RouteManifest{Schema: "public"})

	live.ReloadFrom(&proxy.RouteManifest{Schema: "app"})

	if got := live.For(httptest.NewRequest(http.MethodGet, "/posts", nil)).Schema; got != "app" {
		t.Errorf("schema = %q, want the reloaded file", got)
	}
}
