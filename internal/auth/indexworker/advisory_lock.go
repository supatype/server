package indexworker

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
)

// advisoryLock is a Postgres session-level advisory lock, held on one connection taken out of
// the pool for as long as the lock is. A session lock belongs to the connection that took it:
// taken and released through the pool, the unlock could run on another connection, where it
// was never held, and the lock would go whenever its own connection was closed.
type advisoryLock struct {
	conn      *sql.Conn
	name      string
	unlockSQL string
}

const unlockAdvisorySQL = "SELECT pg_advisory_unlock(hashtext($1)::bigint)"

// tryAdvisoryLock takes the named lock if no other session holds it. release frees it and
// returns the connection to the pool.
func tryAdvisoryLock(ctx context.Context, db *sql.DB, name string) (lock *advisoryLock, acquired bool, err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtext($1)::bigint)", name).Scan(&acquired)
	if err != nil || !acquired {
		return nil, acquired, errors.Join(err, conn.Close())
	}
	return &advisoryLock{conn: conn, name: name, unlockSQL: unlockAdvisorySQL}, true, nil
}

// release unlocks on the connection that holds the lock, reporting whether it was held. When
// the unlock fails the connection may still hold the lock, so it is not handed back to the
// pool: it is closed, which ends the session and the lock with it.
func (l *advisoryLock) release(ctx context.Context) (bool, error) {
	var unlocked bool
	err := l.conn.QueryRowContext(ctx, l.unlockSQL, l.name).Scan(&unlocked)
	if err != nil {
		// ErrBadConn from Raw makes database/sql close the driver connection, not reuse it. That
		// also releases the Conn, so there is nothing left to Close.
		_ = l.conn.Raw(func(any) error { return driver.ErrBadConn })
		return unlocked, err
	}
	return unlocked, l.conn.Close()
}
