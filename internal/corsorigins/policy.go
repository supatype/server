package corsorigins

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// DefaultTTL is how long a replica trusts what it last read. A list saved through one replica
// reaches every other one within this.
const DefaultTTL = 10 * time.Second

// Saved is a list saved from Studio.
type Saved struct {
	Origins   []string
	UpdatedAt time.Time
}

// Store keeps the saved list in the project's database, so it outlives the process and every
// replica reads the same one.
type Store interface {
	// Load returns the saved list, or nil when none is saved.
	Load(ctx context.Context) (*Saved, error)
	Save(ctx context.Context, origins []string) (*Saved, error)
	Delete(ctx context.Context) error
}

// Defaults are the origins in force when nothing is saved, and the platform's own.
type Defaults struct {
	// SiteURL is the project's site URL. Its origin is always part of the default.
	SiteURL string
	// Configured is SUPATYPE_CORS_ALLOW_ORIGINS: config and environment.
	Configured []string
	// Platform origins are allowed whatever is saved.
	Platform []string
}

// Policy answers which origins are in force, reading the saved list through a short cache.
type Policy struct {
	store    Store
	defaults Defaults
	ttl      time.Duration
	now      func() time.Time

	mu       sync.Mutex
	saved    *Saved
	readAt   time.Time
	readOnce bool
}

// NewPolicy builds a policy. A nil store means nothing can be saved, and the default is the answer.
func NewPolicy(store Store, defaults Defaults) *Policy {
	return &Policy{store: store, defaults: defaults, ttl: DefaultTTL, now: time.Now}
}

// lenient reads configured entries the way an operator writes them: `*` as is, anything else
// reduced to its origin, so `https://app.example.com/` matches the header a browser sends.
func lenient(list []string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if strings.TrimSpace(v) == Wildcard {
			out = append(out, Wildcard)
		} else if o := OriginOf(v); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// DefaultOrigins is the default for a request whose host configuration adds `extra` (Cloud's
// tenant manifest): the site URL, then configuration, then extra.
func (p *Policy) DefaultOrigins(extra []string) []string {
	return Union(lenient([]string{p.defaults.SiteURL}), lenient(p.defaults.Configured), lenient(extra))
}

// PlatformOrigins are allowed whatever is saved.
func (p *Policy) PlatformOrigins() []string { return lenient(p.defaults.Platform) }

// SiteOrigin is the origin of the site URL, or "".
func (p *Policy) SiteOrigin() string { return OriginOf(p.defaults.SiteURL) }

// ConfiguredOrigins is the configured part of the default, as read.
func (p *Policy) ConfiguredOrigins() []string { return lenient(p.defaults.Configured) }

// savedList returns the saved list, re-reading it when the cached copy is older than the TTL. A
// read that fails keeps the last known answer rather than falling back to the default: a database
// blip must not quietly reopen a list an admin narrowed, or close one they widened.
func (p *Policy) savedList(ctx context.Context) *Saved {
	if p.store == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readOnce && p.now().Sub(p.readAt) < p.ttl {
		return p.saved
	}
	saved, err := p.store.Load(ctx)
	if err != nil {
		logrus.WithError(err).Warn("cors: could not read the saved origins; keeping the last known list")
		return p.saved
	}
	p.saved, p.readAt, p.readOnce = saved, p.now(), true
	return saved
}

// Snapshot is what is in force, and why.
type Snapshot struct {
	// Origins is every origin allowed now, the platform's and the site URL included.
	Origins []string
	// Source is "saved" when a Studio list is in force, else "default".
	Source string
	// Saved is the saved list, when there is one.
	Saved *Saved
	// Default is what is in force without a saved list (platform origins excluded).
	Default []string
}

// InForce answers for one request; extra is the host's per-request configured origins.
func (p *Policy) InForce(ctx context.Context, extra []string) Snapshot {
	def := p.DefaultOrigins(extra)
	snap := Snapshot{Source: "default", Default: def}
	chosen := def
	if saved := p.savedList(ctx); saved != nil {
		snap.Source, snap.Saved, chosen = "saved", saved, saved.Origins
	}
	// The site URL is always allowed, like the platform's origins (SHc): the project's own app must
	// not be locked out by a list saved without it.
	snap.Origins = Union(p.PlatformOrigins(), lenient([]string{p.defaults.SiteURL}), chosen)
	return snap
}

// Save validates and saves a list, and makes it the answer on this replica at once.
func (p *Policy) Save(ctx context.Context, origins []string) (*Saved, error) {
	normalised, err := Normalise(origins)
	if err != nil {
		return nil, err
	}
	if p.store == nil {
		return nil, ErrNoStore
	}
	saved, err := p.store.Save(ctx, normalised)
	if err != nil {
		return nil, err
	}
	p.remember(saved)
	return saved, nil
}

// Reset removes the saved list, so the default is in force again.
func (p *Policy) Reset(ctx context.Context) error {
	if p.store == nil {
		return ErrNoStore
	}
	if err := p.store.Delete(ctx); err != nil {
		return err
	}
	p.remember(nil)
	return nil
}

func (p *Policy) remember(saved *Saved) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saved, p.readAt, p.readOnce = saved, p.now(), true
}
