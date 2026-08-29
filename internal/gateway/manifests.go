package gateway

// Which route manifest a request is answered from.
//
// A standalone deployment has one, read from a file and re-read when the file
// changes. A managed pod serving a single project merges that file with what
// the control plane published to Valkey. A managed pod serving many projects
// resolves one per calling tenant instead.
//
// Answering from the wrong one sends a tenant's REST traffic to another
// tenant's schema, so the rules live here rather than inside the bootstrap,
// where nothing could reach them without a database and a Valkey.

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/proxy"
)

// tenantHeader carries the calling project's ref on a managed deployment. The
// gateway trusts it only because the tenant HMAC middleware ran first.
const tenantHeader = "X-Supatype-Tenant"

// tenantManifests is the per-tenant lookup — *valkey.TenantManifestCache in a
// running server, and an interface here so the rules above can be tested
// without one.
type tenantManifests interface {
	Get(ctx context.Context, ref string) (*proxy.RouteManifest, error)
	Flush()
}

// mergeManifest folds what the control plane published over the file manifest.
type mergeManifest func(context.Context, *proxy.RouteManifest) (*proxy.RouteManifest, error)

type liveManifests struct {
	// file is the manifest as last read from disk; live is what requests are
	// answered from, which is the same value unless a merge produced another.
	file atomic.Value
	live atomic.Value

	// Both nil in a standalone deployment.
	tenant tenantManifests
	merge  mergeManifest
}

func newLiveManifests(file *proxy.RouteManifest) *liveManifests {
	l := &liveManifests{}
	l.file.Store(file)
	l.live.Store(file)
	return l
}

// File is the manifest as last read from disk, before any merge and without
// the per-tenant lookup: what the health probes are configured from.
func (l *liveManifests) File() *proxy.RouteManifest {
	return l.file.Load().(*proxy.RouteManifest)
}

// Base is what a per-tenant lookup starts from. It hands back a copy, because
// the cache stores what it is given and a tenant's overrides must not be
// written into the deployment's own manifest.
func (l *liveManifests) Base() *proxy.RouteManifest {
	return proxy.CloneRouteManifest(l.File())
}

// Reapply records a manifest read from the file and recomputes what requests
// are answered from.
func (l *liveManifests) Reapply(file *proxy.RouteManifest) {
	l.file.Store(file)
	if l.tenant != nil {
		l.tenant.Flush()
	}
	if l.merge != nil {
		merged, err := l.merge(context.Background(), file)
		if err != nil {
			// The previous live manifest is kept: a Valkey that is briefly away
			// is not a reason to serve every tenant the file's defaults.
			logrus.WithError(err).Warn("serve: Valkey manifest merge failed — keeping previous live manifest")
			return
		}
		l.live.Store(merged)
		return
	}
	l.live.Store(file)
}

// ReloadFrom is what the file watcher calls: the manifest on disk changed, so
// recompute what requests are answered from.
func (l *liveManifests) ReloadFrom(file *proxy.RouteManifest) {
	l.Reapply(file)
	logrus.Info("serve: route manifest reloaded")
}

// For resolves the manifest one request is answered from.
func (l *liveManifests) For(req *http.Request) *proxy.RouteManifest {
	if l.tenant != nil && req != nil {
		if ref := strings.TrimSpace(req.Header.Get(tenantHeader)); ref != "" {
			m, err := l.tenant.Get(req.Context(), ref)
			switch {
			case err != nil:
				logrus.WithError(err).WithField("tenant", ref).Debug("serve: tenant manifest from Valkey failed")
			case m != nil:
				return m
			}
		}
	}
	return l.live.Load().(*proxy.RouteManifest)
}
