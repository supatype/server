// Package site serves a project's static site from local disk, following the deployment the control
// plane says is live.
//
// A Syncer is told the wanted deployment (Want), downloads what it lacks from the control plane into
// an emptyDir, builds the version as a folder of hard links to content-addressed blobs, and swaps the
// handler to it in one pointer store. internal/static does the serving, so MIME types, precompressed
// files, SPA fallback, 404.html and cache headers are the ones a site always had.
package site

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/static"
)

// Config is how a Syncer reaches the control plane and where it keeps files.
type Config struct {
	// Dir is the root of the site emptyDir: blobs/ and versions/.
	Dir string
	// SourceURL is the control plane's base URL. It comes from the environment, never the keyspace.
	SourceURL string
	// TenantID is this project's id as the control plane's internal routes name it.
	TenantID string
	// MaxBytes refuses a deployment whose declared files total more than this.
	MaxBytes int64
	// Cache returns the Cache-Control policy the static handler applies, read when a version is
	// swapped in. Nil means the defaults.
	Cache func() static.CacheOpts
	// RetireAfter is how long a version that left the kept set stays on disk, so a request that
	// started on it can finish. Zero removes it at once.
	RetireAfter time.Duration
	// MinBackoff and MaxBackoff bound the wait between failed attempts (default 1s and 60s).
	MinBackoff, MaxBackoff time.Duration
	// Client fetches from the control plane. Nil uses a default.
	Client *http.Client
}

// Status is what the health detail reports. It never affects readiness.
type Status struct {
	Want    string `json:"want"`
	Serving string `json:"serving"`
	Error   string `json:"error,omitempty"`
}

// served is the version requests are answered from.
type served struct {
	id      string
	handler http.Handler
}

// Syncer keeps the served version equal to the wanted one.
type Syncer struct {
	cfg Config

	want    atomic.Pointer[proxy.SiteRef]
	serve   atomic.Pointer[served]
	wake    chan struct{}
	lastErr atomic.Pointer[string]

	// mu serialises Reconcile, so one worker acts on the latest target at a time, and guards
	// previous, the version served before the current one.
	mu       sync.Mutex
	previous string
	retiring map[string]bool
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// New prepares the folder layout under cfg.Dir.
func New(cfg Config) (*Syncer, error) {
	if cfg.Dir == "" || cfg.SourceURL == "" || cfg.TenantID == "" {
		return nil, errors.New("site: Dir, SourceURL and TenantID are required")
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = time.Minute
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 5 * time.Minute}
	}
	for _, sub := range []string{"blobs", "versions"} {
		if err := os.MkdirAll(filepath.Join(cfg.Dir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("site: %w", err)
		}
	}
	return &Syncer{cfg: cfg, wake: make(chan struct{}, 1), retiring: map[string]bool{}}, nil
}

// Want records the deployment to serve (nil for none) and wakes the worker. It never blocks.
func (s *Syncer) Want(ref *proxy.SiteRef) {
	if ref == nil {
		s.want.Store(nil)
	} else {
		cp := *ref
		s.want.Store(&cp)
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Status reports the wanted and served deployment ids and the last sync error.
func (s *Syncer) Status() Status {
	st := Status{}
	if w := s.want.Load(); w != nil {
		st.Want = w.DeploymentID
	}
	if v := s.serve.Load(); v != nil {
		st.Serving = v.id
	}
	if e := s.lastErr.Load(); e != nil {
		st.Error = *e
	}
	return st
}

// Handler answers site requests: 404 when no site is configured, 503 while the wanted one has not
// synced yet, otherwise the served version. A request loads the served version once.
func (s *Syncer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := s.serve.Load(); v != nil {
			v.handler.ServeHTTP(w, r)
			return
		}
		if s.want.Load() == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Retry-After", "5")
		http.Error(w, "site is being prepared", http.StatusServiceUnavailable)
	})
}

// Run follows Want until ctx ends, retrying a failed sync with backoff from MinBackoff to MaxBackoff.
// A new target resets the backoff.
func (s *Syncer) Run(ctx context.Context) {
	backoff := s.cfg.MinBackoff
	var failedFor string
	for {
		err := s.Reconcile(ctx)
		if ctx.Err() != nil {
			return
		}
		wait := time.Duration(0)
		if err != nil {
			id := s.Status().Want
			if id != failedFor {
				backoff, failedFor = s.cfg.MinBackoff, id
			}
			logrus.WithError(err).WithField("deployment", id).Warn("site: sync failed, serving the previous version")
			wait = backoff
			backoff = min(backoff*2, s.cfg.MaxBackoff)
		} else {
			backoff, failedFor = s.cfg.MinBackoff, ""
		}
		if !s.sleep(ctx, wait) {
			return
		}
	}
}

// sleep waits for d, then for a wake-up when d is zero, and reports false when ctx ended. A Want
// during the wait cuts it short.
func (s *Syncer) sleep(ctx context.Context, d time.Duration) bool {
	if d == 0 {
		select {
		case <-ctx.Done():
			return false
		case <-s.wake:
			return true
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-s.wake:
		return true
	case <-t.C:
		return true
	}
}

// Reconcile makes one attempt to serve the wanted deployment and records the outcome. On failure the
// version already served stays served.
func (s *Syncer) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := s.want.Load()
	if want == nil {
		s.serve.Store(nil)
		s.setError(nil)
		return nil
	}
	if cur := s.serve.Load(); cur != nil && cur.id == want.DeploymentID {
		s.setError(nil)
		return nil
	}
	err := s.syncLocked(ctx, want)
	s.setError(err)
	return err
}

func (s *Syncer) setError(err error) {
	if err == nil {
		s.lastErr.Store(nil)
		return
	}
	msg := err.Error()
	s.lastErr.Store(&msg)
}

// syncLocked brings ref's version onto disk, unless a complete one is already there, and swaps to it.
func (s *Syncer) syncLocked(ctx context.Context, ref *proxy.SiteRef) error {
	if !idPattern.MatchString(ref.DeploymentID) {
		return fmt.Errorf("site: deployment id %q is not valid", ref.DeploymentID)
	}
	if !s.versionComplete(ref.DeploymentID) {
		if err := s.build(ctx, ref); err != nil {
			return err
		}
	}
	// Cleared before the swap, so a reader never sees the new version served beside the old error.
	s.setError(nil)
	s.swapTo(ref)
	s.prune()
	return nil
}

func (s *Syncer) swapTo(ref *proxy.SiteRef) {
	if cur := s.serve.Load(); cur != nil {
		s.previous = cur.id
	}
	dir := s.versionDir(ref.DeploymentID)
	var cache static.CacheOpts
	if s.cfg.Cache != nil {
		cache = s.cfg.Cache()
	}
	s.serve.Store(&served{id: ref.DeploymentID, handler: static.Handler(dir, ref.SPA, cache)})
}
