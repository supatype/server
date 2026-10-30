package indexworker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// heldElsewhere reports whether another session holds the named advisory lock.
func heldElsewhere(t *testing.T, dsn, name string) bool {
	t.Helper()
	other, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var free bool
	q := "SELECT pg_try_advisory_lock(hashtext($1)::bigint) AND pg_advisory_unlock(hashtext($1)::bigint)"
	if err := other.QueryRow(q, name).Scan(&free); err != nil {
		t.Fatal(err)
	}
	return !free
}

// A session lock belongs to the connection that took it. Taken and released through a pool,
// the lock goes with whichever connection ran the query: it is dropped when that connection
// closes, and the unlock may run on another connection, where it was never held.
func TestAdvisoryLockHoldsOneConnectionUntilReleased(t *testing.T) {
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the advisory lock test against Postgres")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxIdleConns(0) // every pooled connection closes once used, as a busy pool can
	ctx := context.Background()
	name := fmt.Sprintf("indexworker_test_%d", time.Now().UnixNano())

	lock, acquired, err := tryAdvisoryLock(ctx, db, name)
	if err != nil || !acquired {
		t.Fatalf("want the lock, got acquired=%v err=%v", acquired, err)
	}
	if !heldElsewhere(t, dsn, name) {
		t.Error("the lock was not held while the work ran")
	}
	unlocked, err := lock.release(ctx)
	if err != nil || !unlocked {
		t.Errorf("want the release to unlock where the lock was taken, got unlocked=%v err=%v", unlocked, err)
	}
	if heldElsewhere(t, dsn, name) {
		t.Error("the lock was still held after release")
	}
}

// When the unlock itself fails, the connection still holds the lock. Handed back to the pool
// it would keep holding it for as long as the pool kept the connection, so it is closed
// instead, which ends the session and the lock with it.
func TestAFailedUnlockClosesTheConnectionThatHoldsTheLock(t *testing.T) {
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the advisory lock test against Postgres")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	name := fmt.Sprintf("indexworker_test_%d", time.Now().UnixNano())
	lock, acquired, err := tryAdvisoryLock(context.Background(), db, name)
	if err != nil || !acquired {
		t.Fatalf("want the lock, got acquired=%v err=%v", acquired, err)
	}

	// An unlock that errors and leaves the connection open, as a cancelled context does not.
	lock.unlockSQL = "SELECT pg_advisory_unlock(hashtext($1)::bigint) / 0"
	_, err = lock.release(context.Background())
	if err == nil {
		t.Fatal("want the unlock to fail")
	}
	// The error is the unlock's alone: closing a connection already closed for it adds nothing.
	if errors.Is(err, sql.ErrConnDone) {
		t.Errorf("release error carries a spurious close error: %v", err)
	}
	if heldElsewhere(t, dsn, name) {
		t.Error("the lock outlived a failed unlock: its connection went back to the pool")
	}
}
