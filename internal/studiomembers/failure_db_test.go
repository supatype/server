package studiomembers

import (
	"context"
	"errors"
	"testing"

	"github.com/supatype/server/internal/data"
)

// Every path in this package denies or reports when the database it reads its
// authority from is unreachable. An admin UI that opens up because its
// authority is missing is worse than one that is briefly unavailable, and a
// membership change that silently does nothing is worse than one that fails.

// dropTables removes the tables the store reads, which is what an unmigrated or
// half-migrated project looks like.
func dropTables(t *testing.T, ctx context.Context, store Store, tables ...string) {
	t.Helper()
	pool, err := store.pool()
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("dropping %s: %v", table, err)
		}
	}
}

// ─── No database at all ───────────────────────────────────────────────────────

// A Store with no resources is what a deployment with no DSN produces. Every
// method has to be callable on it.
func TestEveryMethodWithoutADatabase(t *testing.T) {
	store := Store{}
	ctx := context.Background()

	if _, err := store.List(ctx); !errors.Is(err, data.ErrNoDatabase) {
		t.Errorf("List: %v", err)
	}
	if err := store.SetRole(ctx, adminA, plainUsr, "editor"); !errors.Is(err, data.ErrNoDatabase) {
		t.Errorf("SetRole: %v", err)
	}
	if err := store.Revoke(ctx, adminA, plainUsr); !errors.Is(err, data.ErrNoDatabase) {
		t.Errorf("Revoke: %v", err)
	}

	// The audit writes report through the log rather than to the caller: a
	// missing trail must not stop a compromised admin being revoked.
	store.Audit(ctx, adminA, plainUsr, "grant", "editor")
	store.AuditElevated(ctx, adminA, "GET", "/studio/x")
}

// A target nobody named is not a user, on either mutation.
func TestAMutationWithNoTarget(t *testing.T) {
	store := Store{}
	ctx := context.Background()

	if err := store.SetRole(ctx, adminA, "   ", "editor"); !errors.Is(err, ErrUnknownUser) {
		t.Errorf("SetRole: %v", err)
	}
	if err := store.Revoke(ctx, adminA, "   "); !errors.Is(err, ErrUnknownUser) {
		t.Errorf("Revoke: %v", err)
	}
}

// ─── A database that will not answer ──────────────────────────────────────────

// A membership table that is not there is an unreachable authority. Listing has
// to report it rather than answering with nobody, which a UI would render as an
// empty project.
func TestListWithNoMembershipTable(t *testing.T) {
	ctx, store := setupMembership(t, "")
	dropTables(t, ctx, store, "_supatype.studio_members")

	if _, err := store.List(ctx); err == nil {
		t.Error("want an error, not an empty list")
	}
}

// The same for the mutations: each reads the table before it writes, so a
// missing one is a failure rather than a change nobody recorded.
func TestMutationsWithNoMembershipTable(t *testing.T) {
	ctx, store := setupMembership(t, "")
	dropTables(t, ctx, store, "_supatype.studio_members")

	if err := store.SetRole(ctx, adminA, plainUsr, "editor"); err == nil {
		t.Error("SetRole: want an error")
	}
	if err := store.Revoke(ctx, adminA, plainUsr); err == nil {
		t.Error("Revoke: want an error")
	}
}

// The user check runs against auth.users, so a project without it fails rather
// than granting access to an id nobody has verified.
func TestSetRoleWithNoUsersTable(t *testing.T) {
	ctx, store := setupMembership(t, "")
	dropTables(t, ctx, store, "auth.users")

	if err := store.SetRole(ctx, adminA, plainUsr, "editor"); err == nil {
		t.Error("want an error")
	}
}

// A pool that has been closed is a process shutting down. Nothing may still
// grant access on the way out.
func TestNothingWorksOnAClosedPool(t *testing.T) {
	ctx, store := setupMembership(t, "")
	pool, err := store.pool()
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()

	if _, err := store.List(ctx); err == nil {
		t.Error("List: want an error")
	}
	if err := store.SetRole(ctx, adminA, plainUsr, "editor"); err == nil {
		t.Error("SetRole: want an error")
	}
	if err := store.Revoke(ctx, adminA, plainUsr); err == nil {
		t.Error("Revoke: want an error")
	}
	if _, ok := store.Lookup(adminA); ok {
		t.Error("Lookup: a closed pool granted access")
	}

	// And the audit writes report through the log rather than panicking.
	store.Audit(ctx, adminA, plainUsr, "grant", "editor")
	store.AuditElevated(ctx, adminA, "GET", "/studio/x")
}

// ─── The audit trail ──────────────────────────────────────────────────────────

// An elevated request is recorded with what it reached, which is the whole
// point of making elevation visible.
func TestAuditElevatedRecordsTheRequest(t *testing.T) {
	ctx, store := setupMembership(t, "")

	// The audit table this package writes carries a detail column.
	pool, err := store.pool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE _supatype.studio_audit
		ADD COLUMN IF NOT EXISTS detail JSONB,
		ALTER COLUMN target_id DROP NOT NULL`); err != nil {
		t.Fatal(err)
	}

	store.AuditElevated(ctx, adminA, "POST", "/studio/v1/sql")

	var action, detail string
	if err := pool.QueryRow(ctx,
		`SELECT action, detail::text FROM _supatype.studio_audit ORDER BY id DESC LIMIT 1`).
		Scan(&action, &detail); err != nil {
		t.Fatal(err)
	}
	if action != "elevated_request" {
		t.Errorf("action = %q", action)
	}
	if detail == "" || detail == "null" {
		t.Errorf("detail = %q, want the method and path", detail)
	}
}

// An elevation with no signed-in actor — the dev bypass — is still recorded,
// with the actor left null rather than an empty string that is not a uuid.
func TestAuditElevatedWithNoActor(t *testing.T) {
	ctx, store := setupMembership(t, "")
	pool, err := store.pool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE _supatype.studio_audit
		ADD COLUMN IF NOT EXISTS detail JSONB,
		ALTER COLUMN target_id DROP NOT NULL`); err != nil {
		t.Fatal(err)
	}

	store.AuditElevated(ctx, "  ", "GET", "/studio/v1/schema")

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM _supatype.studio_audit WHERE actor_id IS NULL`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("rows with no actor = %d, want 1", count)
	}
}

// A membership change with no signed-in actor is recorded too, with a null
// actor and a null role where there is none.
func TestAuditWithNoActorOrRole(t *testing.T) {
	ctx, store := setupMembership(t, "")

	store.Audit(ctx, "  ", plainUsr, "revoke", "   ")

	pool, err := store.pool()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM _supatype.studio_audit
		  WHERE actor_id IS NULL AND role IS NULL AND action = 'revoke'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("rows = %d, want the nulls preserved", count)
	}
}

// A missing audit table is noisy rather than fatal. Refusing to revoke a
// compromised admin because the trail is broken would be the worse failure.
func TestAuditingWithNoAuditTable(t *testing.T) {
	ctx, store := setupMembership(t, "")
	dropTables(t, ctx, store, "_supatype.studio_audit")

	store.Audit(ctx, adminA, plainUsr, "grant", "editor")
	store.AuditElevated(ctx, adminA, "GET", "/studio/x")

	// And the mutation it accompanies still works.
	if err := store.SetRole(ctx, adminA, plainUsr, "editor"); err != nil {
		t.Errorf("a broken audit trail blocked the change: %v", err)
	}
}

// ─── Lookup ───────────────────────────────────────────────────────────────────

// A row with no role names no capability, so it is a denial rather than a grant
// of the empty role.
func TestARowWithAnEmptyRoleDenies(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+plainUsr+`', '   ')`)
	_ = ctx

	if role, ok := store.Lookup(plainUsr); ok || role != "" {
		t.Errorf("got (%q, %v), want a denial", role, ok)
	}
}

// ─── Rows and writes that will not go ─────────────────────────────────────────

// A membership table whose columns are not what this code expects is a
// half-applied migration. Reporting beats returning a list of zero values that
// a UI would render as real members.
func TestListWithAColumnItCannotRead(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+plainUsr+`', 'editor')`)

	pool, err := store.pool()
	if err != nil {
		t.Fatal(err)
	}
	// An array where a scalar was, which pgx will not scan into a string.
	if _, err := pool.Exec(ctx,
		`ALTER TABLE _supatype.studio_members ALTER COLUMN role TYPE text[] USING ARRAY[role]`); err != nil {
		t.Fatal(err)
	}

	if _, err := store.List(ctx); err == nil {
		t.Error("want an error rather than a row of zero values")
	}
}

// A write the database refuses is a change that did not happen, and the caller
// has to be told rather than shown a success.
func TestAGrantTheDatabaseRefuses(t *testing.T) {
	ctx, store := setupMembership(t, "")

	pool, err := store.pool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE _supatype.studio_members
		ADD CONSTRAINT known_role CHECK (role IN ('admin', 'editor', 'viewer'))`); err != nil {
		t.Fatal(err)
	}

	if err := store.SetRole(ctx, adminA, plainUsr, "sudo"); err == nil {
		t.Error("want an error")
	}
	if role := roleOf(t, store, plainUsr); role != "" {
		t.Errorf("the refused role was stored: %q", role)
	}
}

// And a revocation the database refuses. Something still referencing the
// membership row is the realistic case.
func TestARevocationTheDatabaseRefuses(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+plainUsr+`', 'editor')`)

	pool, err := store.pool()
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE _supatype.studio_member_notes (
			member_id UUID NOT NULL REFERENCES _supatype.studio_members (id) ON DELETE RESTRICT,
			note TEXT)`,
		`INSERT INTO _supatype.studio_member_notes (member_id, note)
		 SELECT id, 'held' FROM _supatype.studio_members WHERE user_id = '` + plainUsr + `'`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS _supatype.studio_member_notes`)
	})

	if err := store.Revoke(ctx, adminA, plainUsr); err == nil {
		t.Error("want an error")
	}
	if role := roleOf(t, store, plainUsr); role != "editor" {
		t.Errorf("the membership went despite the refusal: %q", role)
	}
}
