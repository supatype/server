package changelog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SlotName is the listener's own replication slot. Separate from realtime's `supatype_realtime`,
// so each keeps its own position and neither can consume the other's changes.
const SlotName = "supatype_hooks"

// Advisory lock key, two-int form. The schema push lock is (872014, 1); this is its own class so
// nothing else can collide with it by accident.
const (
	readerLockClass = 872015
	readerLockID    = 1
	pushLockClass   = 872014
	pushLockID      = 1
)

// batchChanges is how many rows one read asks for. Postgres stops at a transaction boundary past
// it, so a batch is always whole transactions.
const batchChanges = 1000

// UnsupportedError means this database cannot feed the listener, and retrying will not change that.
type UnsupportedError struct {
	Reason string
	Err    error
}

func (e *UnsupportedError) Error() string { return "change-log after hooks unavailable: " + e.Reason }
func (e *UnsupportedError) Unwrap() error { return e.Err }

// unsupportedReason classifies a slot error the way realtime does, from the SQLSTATE measured on
// supatype/postgres, or "" when the error is something else.
func unsupportedReason(err error) string {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return ""
	}
	// Checked before the code: the image's plugin allowlist refuses with the same 42501 a role
	// without REPLICATION gets, and the remedies differ.
	if strings.Contains(pgErr.Message, "may not be used as an output plugin") {
		return "wal2json is not in output_plugin_libraries"
	}
	switch pgErr.Code {
	case "55000":
		return "wal_level is not logical"
	case "58P01":
		return "the wal2json output plugin is not installed"
	case "42501":
		return "the database role may not use replication slots"
	}
	return ""
}

func classify(err error) error {
	if reason := unsupportedReason(err); reason != "" {
		return &UnsupportedError{Reason: reason, Err: err}
	}
	return err
}

// slot is the listener's SQL surface on one session.
type slot struct {
	conn *pgx.Conn
	name string
}

// exists reports whether the slot has been created.
func (s slot) exists(ctx context.Context) (bool, error) {
	var exists bool
	err := s.conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)`, s.name).Scan(&exists)
	return exists, err
}

// ensure creates the slot when it does not exist. Every replica runs this; a create that loses the
// race to another replica is not an error.
func (s slot) ensure(ctx context.Context) error {
	exists, err := s.exists(ctx)
	if err != nil {
		return classify(err)
	}
	if exists {
		return nil
	}
	return s.create(ctx)
}

// create makes the slot. Losing the race to another replica that created it first (42710) is not
// an error.
func (s slot) create(ctx context.Context) error {
	_, err := s.conn.Exec(ctx, `SELECT pg_create_logical_replication_slot($1, 'wal2json')`, s.name)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42710" {
		return nil
	}
	return classify(err)
}

// tryLock takes the reader lock for this session, reporting whether it is now held. Session-level,
// so a reader that dies or loses its connection frees it for a standby to take over.
func (s slot) tryLock(ctx context.Context) (bool, error) {
	var held bool
	err := s.conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1, $2)`, readerLockClass, readerLockID).Scan(&held)
	return held, err
}

// pushLockHeld reports whether a schema push is running. Reads wait for it, so a table's restricted
// columns are not judged by a manifest the push is about to replace.
func (s slot) pushLockHeld(ctx context.Context) (bool, error) {
	var held bool
	err := s.conn.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_locks
		WHERE locktype = 'advisory' AND classid = $1 AND objid = $2 AND granted)`,
		pushLockClass, pushLockID).Scan(&held)
	return held, err
}

// peekQuery reads without consuming. The slot moves only through advance, once the changes are
// delivered, so a crash between the two re-reads rather than loses.
func peekQuery() string {
	opts := make([]string, len(SlotOptions))
	for i, o := range SlotOptions {
		opts[i] = "'" + o + "'"
	}
	return "SELECT lsn::text, data FROM pg_logical_slot_peek_changes($1, NULL, $2, " + strings.Join(opts, ", ") + ")"
}

// peek streams up to a batch of rows to fn and returns the LSN of the last one, or "" for none.
//
// The query's own error surfaces through ForEachRow, which reads rows.Err, so one check covers a
// failed query, a failed scan and an error from fn.
func (s slot) peek(ctx context.Context, fn func(data []byte) error) (string, error) {
	rows, _ := s.conn.Query(ctx, peekQuery(), s.name, batchChanges)
	var lsn, last string
	var data []byte
	_, err := pgx.ForEachRow(rows, []any{&lsn, &data}, func() error {
		last = lsn
		return fn(data)
	})
	if err != nil {
		return "", classify(err)
	}
	return last, nil
}

// advance moves the slot to lsn, releasing the WAL behind it.
func (s slot) advance(ctx context.Context, lsn string) error {
	if _, err := s.conn.Exec(ctx, `SELECT pg_replication_slot_advance($1, $2::pg_lsn)`, s.name, lsn); err != nil {
		return fmt.Errorf("advancing %s to %s: %w", s.name, lsn, err)
	}
	return nil
}

// retainedWAL is how many bytes of WAL the slot is holding on disk, for doctor.
func (s slot) retainedWAL(ctx context.Context) (int64, error) {
	var bytes int64
	err := s.conn.QueryRow(ctx, `SELECT COALESCE(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn), 0)::bigint
		FROM pg_replication_slots WHERE slot_name = $1`, s.name).Scan(&bytes)
	return bytes, err
}
