package storage

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/gobuffalo/pop/v6"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// Wait for Postgres instead of refusing to start when it is not there yet.
//
// `New` used to return the dial error, which exits the binary. In a Compose stack the `db`
// healthcheck hid that: `depends_on: service_healthy` held the container back until Postgres
// answered. Two things break the arrangement — a stack pointed at an external database, which has no
// `db` container to wait on, and a database that restarts *under* a running server, which no
// healthcheck ever covered. Both presented identically: the container gone, one line in the log.
//
// Only connection-level failures are retried. A wrong password or a missing database is a fact about
// the configuration, not a timing problem, and retrying it forever would bury the one message that
// says what to fix.
//
// `packages/storage/src/db-retry.ts` and `packages/realtime/src/db-retry.ts` in the monorepo are the
// same policy for the same reason.

const (
	dialFirstDelay = 500 * time.Millisecond
	dialMaxDelay   = 10 * time.Second
)

// transientSQLStates are the SQLSTATEs that mean "not reachable yet", not "wrong":
// class 08 (connection exception), 57P01/02/03 (admin shutdown, crash shutdown, cannot connect now),
// and 53300 (too many connections).
var transientSQLStates = map[string]struct{}{
	"08000": {}, "08001": {}, "08003": {}, "08004": {}, "08006": {}, "08007": {}, "08P01": {},
	"57P01": {}, "57P02": {}, "57P03": {},
	"53300": {},
}

// isTransientDialError reports whether a failed dial is worth retrying.
func isTransientDialError(err error) bool {
	if err == nil {
		return false
	}

	var pgxErr *pgconn.PgError
	if errors.As(err, &pgxErr) {
		_, ok := transientSQLStates[pgxErr.Code]
		return ok
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		_, ok := transientSQLStates[string(pqErr.Code)]
		return ok
	}

	// No SQLSTATE means the connection never got far enough to receive one: DNS, TCP, TLS.
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// pop wraps the driver's error into a string in places, so a narrow set of messages is matched
	// as well. Narrow deliberately: matching loosely here turns a misconfiguration into a hang.
	msg := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"connection refused",
		"no such host",
		"i/o timeout",
		"connection reset by peer",
		"network is unreachable",
		"host is unreachable",
		"the database system is starting up",
		"the database system is shutting down",
		"the database system is in recovery mode",
		"failed to connect to",
		"dial tcp",
	} {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}

// dialFunc is the dial being retried; swapped in tests.
type dialFunc func(context.Context) (*Connection, error)

// retryTransient runs attempt until it succeeds, until the failure is one that
// waiting cannot fix, or until ctx is done.
//
// Every attempt is logged with the elapsed total, so a database that has been
// unreachable for ten minutes reads as exactly that rather than as a server
// that quietly stopped trying.
func retryTransient(ctx context.Context, attempt func(context.Context) error) error {
	started := time.Now()
	delay := dialFirstDelay

	for n := 1; ; n++ {
		err := attempt(ctx)
		if err == nil {
			if n > 1 {
				logrus.WithField("attempts", n).Info("storage: database reachable")
			}
			return nil
		}
		if !isTransientDialError(err) {
			return err
		}
		// A cancelled context means shutdown, and pgx reports that as a connection failure. Return
		// it rather than looping against a server that is going away.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return err
		}

		logrus.WithError(err).
			WithField("attempt", n).
			WithField("elapsed", time.Since(started).Round(time.Second).String()).
			WithField("retry_in", delay.String()).
			Warn("storage: database not reachable, retrying")

		select {
		case <-ctx.Done():
			return err
		case <-time.After(delay):
		}
		if delay *= 2; delay > dialMaxDelay {
			delay = dialMaxDelay
		}
	}
}

// DialWithRetry dials the database, waiting through transient failures until ctx is done.
func DialWithRetry(ctx context.Context, dial dialFunc) (*Connection, error) {
	var conn *Connection
	err := retryTransient(ctx, func(ctx context.Context) error {
		var dialErr error
		conn, dialErr = dial(ctx)
		return dialErr
	})
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// WaitReachable blocks until the database answers, waiting out the same
// transient failures DialWithRetry does.
//
// For callers that build their own pop connection rather than going through
// DialContext. pop's Open builds a pool and contacts nothing, so without this
// the first real contact is a query, and a database that is merely still
// starting takes the process down with it.
func WaitReachable(ctx context.Context, db *pop.Connection) error {
	sqldb, ok := popConnToStd(db)
	if !ok || sqldb == nil {
		// Nothing to ping. The caller's first query is then the first contact,
		// which is where things were before this existed.
		return nil
	}
	return retryTransient(ctx, sqldb.PingContext)
}
