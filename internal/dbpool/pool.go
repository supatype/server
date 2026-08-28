// Package dbpool provides the process-wide Postgres pool used by server-side
// features that read the project database directly: the Studio SQL runner and
// Studio membership lookups.
//
// One pool, opened lazily on first use, because these features are optional: a
// deployment without a DSN must still start and serve everything else. Callers
// get a plain error when no DSN is configured and are expected to fail closed.
//
// The DSN arrives through Configure rather than being read from the environment
// here. This package used to consult SUPATYPE_SQL_DATABASE_URL and DATABASE_URL
// itself, which made it a second, independent reader of a connection string the
// auth service already loads through its own configuration.
package dbpool

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// ErrNoDSN is returned when no connection string has been configured.
var ErrNoDSN = errors.New("no database DSN configured (SUPATYPE_SQL_DATABASE_URL or DATABASE_URL)")

// maxConns is shared by every admin feature on this pool, and those have very
// different shapes: the SQL runner allows 30-second queries returning up to
// 10,000 rows, while a Studio capability check is a 3-second-bounded primary-key
// read. A saturated pool makes the capability check time out and fail closed,
// a spurious 403 on Studio while someone else runs slow SQL, so leave enough
// headroom that saturation takes deliberate abuse rather than ordinary use.
const maxConns = 10

var (
	mu      sync.RWMutex
	dsn     string
	once    sync.Once
	pool    *pgxpool.Pool
	poolErr error
)

// Configure records the connection string these features should use. It is
// called once at startup, before any request can reach a handler that needs the
// pool.
//
// Calling it after the pool has opened has no effect on the open pool, which is
// the same behaviour the environment read had: the first DSN seen wins for the
// life of the process.
func Configure(connString string) {
	mu.Lock()
	defer mu.Unlock()
	dsn = connString
}

// DSN returns the configured connection string.
func DSN() string {
	mu.RLock()
	defer mu.RUnlock()
	return dsn
}

// Pool returns the shared pool, opening it on first call.
//
// The returned error is stable across calls: it can only come from a missing or
// unparseable DSN, both of which are fixed at startup. Reaching the database is
// not attempted here, since pgxpool connects lazily, so a database that is
// merely slow to come up does not permanently poison the pool.
func Pool(context.Context) (*pgxpool.Pool, error) {
	once.Do(func() {
		connString := DSN()
		if connString == "" {
			poolErr = ErrNoDSN
			return
		}
		cfg, err := pgxpool.ParseConfig(connString)
		if err != nil {
			poolErr = err
			return
		}
		cfg.MaxConns = maxConns
		// Background context: pool lifetime must not be tied to the first
		// request's context.
		pool, poolErr = pgxpool.NewWithConfig(context.Background(), cfg)
		if poolErr != nil {
			logrus.WithError(poolErr).WithField("dsn_host", cfg.ConnConfig.Host).
				Error("dbpool: failed to open pool")
			return
		}
		logrus.WithField("dsn_host", cfg.ConnConfig.Host).Info("dbpool: pool opened")
	})
	return pool, poolErr
}
