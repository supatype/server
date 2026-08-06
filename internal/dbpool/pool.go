// Package dbpool provides the process-wide Postgres pool used by server-side
// features that read the project database directly — the Studio SQL runner and
// Studio membership lookups.
//
// One pool, opened lazily on first use, because these features are optional: a
// deployment without a DSN must still start and serve everything else. Callers
// get a plain error when no DSN is configured and are expected to fail closed.
package dbpool

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

// ErrNoDSN is returned when neither DSN environment variable is set.
var ErrNoDSN = errors.New("no database DSN configured (SUPATYPE_SQL_DATABASE_URL or DATABASE_URL)")

const maxConns = 5

var (
	once    sync.Once
	pool    *pgxpool.Pool
	poolErr error
)

// DSN returns the configured connection string, preferring the explicit
// Supatype variable over the generic one so a deployment can point admin
// features at a different role than the app's.
func DSN() string {
	if dsn := os.Getenv("SUPATYPE_SQL_DATABASE_URL"); dsn != "" {
		return dsn
	}
	return os.Getenv("DATABASE_URL")
}

// Pool returns the shared pool, opening it on first call.
//
// The returned error is stable across calls: it can only come from a missing or
// unparseable DSN, both of which are fixed at startup. Reaching the database is
// not attempted here — pgxpool connects lazily — so a database that is merely
// slow to come up does not permanently poison the pool.
func Pool(context.Context) (*pgxpool.Pool, error) {
	once.Do(func() {
		dsn := DSN()
		if dsn == "" {
			poolErr = ErrNoDSN
			return
		}
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			poolErr = err
			return
		}
		cfg.MaxConns = maxConns
		// Background context — pool lifetime must not be tied to the first
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
