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
	"github.com/supatype/server/internal/data/valkey"
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

// openValkey is the Valkey constructor, indirected so tests can exercise the
// path where a cache connects. valkey.New dials eagerly, so without this the
// only reachable branch in a unit test is the failure one.
var openValkey = valkey.New

// Resources is every connection the process owns, and the one place that closes
// them.
type Resources struct {
	// cache is reached through Cache, which is nil-safe in both directions: a nil
	// Resources and an unset field both yield the unavailable client, so no
	// consumer needs a nil check of its own.
	cache valkey.Client

	// admin serves the Studio SQL runner and Studio membership. It is nil when
	// no connection string was configured; reach it through AdminPool.
	admin *pgxpool.Pool

	// closers run in reverse order of registration, so a resource is never torn
	// down before something that depends on it.
	closers []func() error
}

// Open acquires the resources described by cfg.
//
// A Valkey that is configured but unreachable is fatal only in managed mode,
// where the tenant manifest lives in it; elsewhere the caches degrade and the
// service runs. A database that is merely absent is never fatal here: the
// features that need it report ErrNoDatabase per request.
func Open(ctx context.Context, cfg *config.Config) (*Resources, error) {
	r := &Resources{cache: valkey.Unavailable()}

	if addr := strings.TrimSpace(cfg.ValkeyAddr); addr != "" {
		client, err := openValkey(addr)
		if err != nil {
			managed := strings.TrimSpace(cfg.Mode) == "managed"
			if managed {
				return nil, fmt.Errorf("data: Valkey connect failed in managed mode: %w", err)
			}
			logrus.WithError(err).Warn("data: Valkey connect failed, caches will bypass")
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
	//
	// The error here is not reachable once ParseConfig has succeeded and MaxConns
	// is a sane constant, which is why this package carries a measured floor
	// rather than a pin. It is kept because pgxpool may grow new reasons to
	// refuse a config, and discovering that by panicking on a nil pool would be
	// worse.
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, err
	}
	logrus.WithField("dsn_host", poolCfg.ConnConfig.Host).Info("data: admin pool opened")
	return pool, nil
}

// Cache returns the Valkey client, never nil.
func (r *Resources) Cache() valkey.Client {
	if r == nil || r.cache == nil {
		return valkey.Unavailable()
	}
	return r.cache
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
	r.cache = valkey.Unavailable()
	return errors.Join(errs...)
}
