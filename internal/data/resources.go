// Package data owns the connections this process holds.
//
// Ownership is the point, not uniformity. The service legitimately keeps more
// than one pool: the auth service talks through pop, the Studio SQL runner and
// membership talk through pgx, and the index worker takes a connection of its
// own so that a long CREATE INDEX cannot starve request handling. What was wrong
// was that each of those decided for itself where its connection string came
// from and when it was closed, and one of them kept a package-level singleton
// nobody could see or replace.
//
// Here, configuration and lifetime have a single owner and the pools stay
// separate for the reasons they exist.
package data

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace"
)

// ErrNoDatabase is returned when a feature needs the admin pool and no
// connection string was configured.
//
// The Studio SQL runner and Studio membership are optional: a deployment
// without a DSN must still start and serve everything else, so this is a plain
// error to fail closed on rather than a reason to refuse to boot.
var ErrNoDatabase = errors.New("data: no database configured (SUPATYPE_SQL_DATABASE_URL or DATABASE_URL)")

// adminMaxConns is shared by every admin feature on the pool, and those have
// very different shapes: the SQL runner allows 30-second queries returning up to
// 10,000 rows, while a Studio capability check is a 3-second-bounded primary-key
// read. A saturated pool makes the capability check time out and fail closed, a
// spurious 403 on Studio while someone else runs slow SQL, so leave enough
// headroom that saturation takes deliberate abuse rather than ordinary use.
const adminMaxConns = 10

// openKeyspace is the keyspace constructor, indirected so tests can exercise
// the path where a cache connects. keyspace.New dials eagerly, so without this
// the only reachable branch in a unit test is the failure one.
var openKeyspace = keyspace.New

// newPool is the pgxpool constructor, indirected for the same reason: once
// ParseConfig has succeeded and MaxConns is a sane constant, it has no reason
// left to refuse, so its error branch is unreachable in production. The branch
// is kept because pgxpool may grow new reasons, and discovering one by
// panicking on a nil pool would be worse.
var newPool = pgxpool.NewWithConfig

// Resources is every connection the process owns, and the one place that closes
// them.
type Resources struct {
	// cache is reached through Cache, which is nil-safe in both directions: a nil
	// Resources and an unset field both yield the unavailable client, so no
	// consumer needs a nil check of its own.
	//
	// It is the *project's* keyspace: the REST response cache and nothing else.
	cache keyspace.Client

	// platform is the keyspace holding platform state — the tenant configuration
	// the control plane publishes, the route manifest merged from it, the wrapped
	// DB credentials, and the MAU day-sets. Reached through PlatformCache.
	//
	// When both addresses resolve to the same server this is the same client as
	// cache, not a second connection to it. Two clients would double the
	// connections for no benefit, and would make the ordinary single-keyspace
	// deployment pay for a split it did not ask for.
	platform keyspace.Client

	// admin serves the Studio SQL runner and Studio membership. It is nil when
	// no connection string was configured; reach it through AdminPool.
	admin *pgxpool.Pool

	// closers run in reverse order of registration, so a resource is never torn
	// down before something that depends on it.
	closers []func() error
}

// Open acquires the resources described by cfg.
//
// The platform keyspace being configured but unreachable is fatal in managed
// mode, where the tenant manifest lives in it; elsewhere the caches degrade and
// the service runs. A database that is merely absent is never fatal here: the
// features that need it report ErrNoDatabase per request.
//
// The project keyspace is never fatal, in any mode. It holds cached responses,
// so its absence costs latency and nothing else — and it usually lives in the
// project's own Postgres, which may still be accepting connections when this pod
// starts. Making it fatal would turn a cold start into a crashloop and take the
// whole gateway down to protect a cache.
func Open(ctx context.Context, cfg *config.Config) (*Resources, error) {
	r := &Resources{cache: keyspace.Unavailable(), platform: keyspace.Unavailable()}

	platformAddr := cfg.KeyspaceAddress()
	if platformAddr != "" {
		client, err := openKeyspace(platformAddr)
		if err != nil {
			managed := strings.TrimSpace(cfg.Mode) == "managed"
			if managed {
				return nil, fmt.Errorf("data: keyspace connect failed in managed mode: %w", err)
			}
			logrus.WithError(err).Warn("data: keyspace connect failed, caches will bypass")
		} else {
			r.platform = client
			r.cache = client
			r.onClose(func() error { client.Close(); return nil })
		}
	}

	// Only when it names a different server. Equal addresses are the
	// single-keyspace deployment, already served by the client above.
	if addr := cfg.ProjectKeyspaceAddress(); addr != "" && addr != platformAddr {
		client, err := openKeyspace(addr)
		if err != nil {
			// Deliberately not fatal, and logged at warn rather than error: the
			// gateway serves every request it would have served, slower.
			logrus.WithError(err).WithField("addr", addr).
				Warn("data: project keyspace connect failed, response cache will bypass")
			r.cache = keyspace.Unavailable()
		} else {
			r.cache = client
			r.onClose(func() error { client.Close(); return nil })
		}
	}

	if dsn := cfg.SQLDSN(); dsn != "" {
		pool, err := openAdminPool(ctx, dsn)
		if err != nil {
			return nil, r.closeAfter(fmt.Errorf("data: admin pool: %w", err))
		}
		r.admin = pool
		r.onClose(func() error { pool.Close(); return nil })
	}

	return r, nil
}

// openAdminPool parses the DSN and builds the pool. Reaching the database is not
// attempted, since pgxpool connects lazily, so a database that is slow to come
// up does not stop the process starting.
func openAdminPool(_ context.Context, dsn string) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	poolCfg.MaxConns = adminMaxConns
	// Background context: pool lifetime must not be tied to the caller's.
	pool, err := newPool(context.Background(), poolCfg)
	if err != nil {
		return nil, err
	}
	logrus.WithField("dsn_host", poolCfg.ConnConfig.Host).Info("data: admin pool opened")
	return pool, nil
}

// Cache returns the project keyspace client — the response cache — never nil.
func (r *Resources) Cache() keyspace.Client {
	if r == nil || r.cache == nil {
		return keyspace.Unavailable()
	}
	return r.cache
}

// PlatformCache returns the platform keyspace client, never nil.
//
// Everything whose only copy is written by the control plane reads through this:
// the tenant configuration, the merged route manifest, the wrapped DB
// credentials, and the MAU day-sets. Reading any of those from the project
// keyspace would find nothing there and mistake it for a tenant that was never
// published.
func (r *Resources) PlatformCache() keyspace.Client {
	if r == nil || r.platform == nil {
		return keyspace.Unavailable()
	}
	return r.platform
}

// AdminPool returns the pool used by the Studio SQL runner and Studio
// membership, or ErrNoDatabase when none is configured.
func (r *Resources) AdminPool() (*pgxpool.Pool, error) {
	if r == nil || r.admin == nil {
		return nil, ErrNoDatabase
	}
	return r.admin, nil
}

// HasDatabase reports whether admin features can reach a database. It decides
// between the membership path and the legacy claim path, so a deployment with no
// DSN is not locked out of its own Studio.
func (r *Resources) HasDatabase() bool {
	return r != nil && r.admin != nil
}

// onClose registers a teardown step.
func (r *Resources) onClose(fn func() error) { r.closers = append(r.closers, fn) }

// closeAfter releases what has been acquired so far and returns cause, for use
// on a failed Open.
func (r *Resources) closeAfter(cause error) error {
	if err := r.Close(); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// Close releases every resource, in reverse order of acquisition, and reports
// all failures rather than the first.
//
// It is safe to call more than once and on a nil receiver, because the shutdown
// paths call it from a defer that cannot know how far Open got.
func (r *Resources) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	for i := len(r.closers) - 1; i >= 0; i-- {
		if err := r.closers[i](); err != nil {
			errs = append(errs, err)
		}
	}
	r.closers = nil
	r.admin = nil
	r.cache = keyspace.Unavailable()
	r.platform = keyspace.Unavailable()
	return errors.Join(errs...)
}
