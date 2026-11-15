package gateway

// Which route manifest a request is answered from.
//
// A standalone deployment has one, read from a file and re-read when the file
// changes. A managed pod serving a single project merges that file with what
// the control plane published to the keyspace. A managed pod serving many projects
// resolves one per calling tenant instead.
//
// Answering from the wrong one sends a tenant's REST traffic to another
// tenant's schema, so the rules live here rather than inside the bootstrap,
// where nothing could reach them without a database and a keyspace.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/proxy"
)

// tenantHeader carries the calling project's ref on a managed deployment. The
// gateway trusts it only because the tenant HMAC middleware ran first.
const tenantHeader = "X-Supatype-Tenant"

// tenantManifests is the per-tenant lookup — *keyspace.TenantManifestCache in a
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

	// mu serialises installs, which come from the file watcher and the keyspace refresh, and guards
	// applied: a hash of the merged manifest last installed, nil when the next read must be applied
	// (before the first one, and after a merge error).
	mu      sync.Mutex
	applied []byte

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
	l.mu.Lock()
	defer l.mu.Unlock()
	l.file.Store(file)
	merged, sum, ok := l.mergeLocked(file)
	if ok {
		l.installLocked(merged, sum)
	}
}

// refresh re-reads the keyspace for the file manifest already held and installs the result only when
// it differs from what was installed last, so a quiet keyspace costs a read and nothing else: no
// tenant cache flush and no new live manifest. The first read, and the first after a failed one, are
// always installed.
func (l *liveManifests) refresh() {
	l.mu.Lock()
	defer l.mu.Unlock()
	merged, sum, ok := l.mergeLocked(l.File())
	if !ok {
		return
	}
	if l.applied != nil && bytes.Equal(sum, l.applied) {
		return
	}
	l.installLocked(merged, sum)
}

// mergeLocked folds the keyspace over file and hashes the result. ok is false when the merge failed,
// in which case the previous live manifest is kept: a keyspace that is briefly away is not a reason to
// serve every tenant the file defaults, and the next good read is installed even if it looks the same.
func (l *liveManifests) mergeLocked(file *proxy.RouteManifest) (merged *proxy.RouteManifest, sum []byte, ok bool) {
	if l.merge == nil {
		return file, nil, true
	}
	merged, err := l.merge(context.Background(), file)
	if err != nil {
		l.applied = nil
		logrus.WithError(err).Warn("serve: keyspace manifest merge failed, keeping previous live manifest")
		return nil, nil, false
	}
	return merged, manifestHash(merged), true
}

func (l *liveManifests) installLocked(merged *proxy.RouteManifest, sum []byte) {
	if l.tenant != nil {
		l.tenant.Flush()
	}
	l.live.Store(merged)
	l.applied = sum
}

// manifestHash identifies a manifest by its content. A manifest that cannot be encoded has no hash,
// which makes every read of it count as changed.
func manifestHash(m *proxy.RouteManifest) []byte {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	sum := sha256.Sum256(raw)
	return sum[:]
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
				logrus.WithError(err).WithField("tenant", ref).Debug("serve: tenant manifest from the keyspace failed")
			case m != nil:
				return m
			}
		}
	}
	return l.live.Load().(*proxy.RouteManifest)
}
