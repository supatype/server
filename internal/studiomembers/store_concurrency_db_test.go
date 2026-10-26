package studiomembers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// holdAdminRows locks every admin membership row in a transaction of its own
// and returns its release. Both changes under test then queue behind it, each
// having started its transaction and reached the rows the guard reads, so
// neither can commit before the other has reached the guard.
func holdAdminRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool) func() {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT 1 FROM _supatype.studio_members WHERE role = 'admin' FOR UPDATE`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("lock admin rows: %v", err)
	}
	return func() { _ = tx.Rollback(ctx) }
}

// waitForLockWaiters waits until `n` sessions in this database are blocked on
// a lock, which is the barrier: both changes are inside their transactions and
// queued behind the blocker.
func waitForLockWaiters(t *testing.T, ctx context.Context, pool *pgxpool.Pool, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatalf("count lock waiters: %v", err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d changes reached the lock", waiting, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Two admins each removing or demoting the other at once, the last two admins
// of the project, both read "two admins" unless the guard locks what it
// counts. Exactly one may win; the other must see its change and be refused,
// or the project is left with nobody able to grant access.
func TestConcurrentChangesCannotRemoveTheLastTwoAdmins(t *testing.T) {
	changes := map[string]func(ctx context.Context, store Store) [2]func() error{
		"revoke and revoke": func(ctx context.Context, store Store) [2]func() error {
			return [2]func() error{
				func() error { return store.Revoke(ctx, plainUsr, adminA) },
				func() error { return store.Revoke(ctx, plainUsr, adminB) },
			}
		},
		"demote and revoke": func(ctx context.Context, store Store) [2]func() error {
			return [2]func() error{
				func() error { return store.SetRole(ctx, plainUsr, adminA, "editor") },
				func() error { return store.Revoke(ctx, plainUsr, adminB) },
			}
		},
		"demote and demote": func(ctx context.Context, store Store) [2]func() error {
			return [2]func() error{
				func() error { return store.SetRole(ctx, plainUsr, adminA, "viewer") },
				func() error { return store.SetRole(ctx, plainUsr, adminB, "editor") },
			}
		},
	}

	for name, build := range changes {
		t.Run(name, func(t *testing.T) {
			ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
				VALUES ('`+adminA+`', 'admin'), ('`+adminB+`', 'admin')`)
			pool, err := store.pool()
			if err != nil {
				t.Fatalf("pool: %v", err)
			}

			release := holdAdminRows(t, ctx, pool)
			results := make(chan error, 2)
			for _, change := range build(ctx, store) {
				go func() { results <- change() }()
			}
			waitForLockWaiters(t, ctx, pool, 2)
			release()

			var succeeded, refused int
			for range 2 {
				switch err := <-results; {
				case err == nil:
					succeeded++
				case errors.Is(err, ErrLastAdmin):
					refused++
				default:
					t.Fatalf("unexpected error: %v", err)
				}
			}
			if succeeded != 1 || refused != 1 {
				t.Fatalf("want one change and one ErrLastAdmin, got %d succeeded and %d refused",
					succeeded, refused)
			}

			var admins int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM _supatype.studio_members WHERE role = 'admin'`).Scan(&admins); err != nil {
				t.Fatalf("count admins: %v", err)
			}
			if admins != 1 {
				t.Fatalf("want one admin left, got %d", admins)
			}
		})
	}
}
