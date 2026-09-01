package studiomembers

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
)

const (
	adminA   = "11111111-1111-1111-1111-111111111111"
	adminB   = "22222222-2222-2222-2222-222222222222"
	plainUsr = "33333333-3333-3333-3333-333333333333"
	cloudAcc = "44444444-4444-4444-4444-444444444444"
	stranger = "55555555-5555-5555-5555-555555555555"
)

// setupMembership builds a project database with `auth.users` and an empty
// membership table, then applies `seed`.
//
// Run the DB-backed packages with `-p 1`: they share one database and the same
// `_supatype` table names, so `go test`'s default per-package parallelism has
// them dropping each other's tables mid-run.
func setupMembership(t *testing.T, seed string) (context.Context, Store) {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run membership mutations against Postgres")
	}
	resources, err := data.Open(context.Background(), &config.Config{SQLDatabaseURL: dsn})
	if err != nil {
		t.Fatalf("open resources: %v", err)
	}
	t.Cleanup(func() { _ = resources.Close() })

	ctx := context.Background()
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}

	stmts := []string{
		`CREATE SCHEMA IF NOT EXISTS _supatype`,
		`CREATE SCHEMA IF NOT EXISTS auth`,
		`DROP TABLE IF EXISTS _supatype.studio_members`,
		`DROP TABLE IF EXISTS _supatype.studio_audit`,
		`DROP TABLE IF EXISTS auth.users`,
		`CREATE TABLE auth.users (id UUID PRIMARY KEY, email TEXT)`,
		`CREATE TABLE _supatype.studio_members (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID UNIQUE,
			platform_user_id UUID UNIQUE,
			role TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT studio_members_one_identity
				CHECK (num_nonnulls(user_id, platform_user_id) = 1))`,
		`CREATE TABLE _supatype.studio_audit (
			id BIGSERIAL PRIMARY KEY,
			actor_id UUID,
			target_id UUID NOT NULL,
			action TEXT NOT NULL,
			role TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`INSERT INTO auth.users (id, email) VALUES
			('` + adminA + `', 'admin-a@example.com'),
			('` + adminB + `', 'admin-b@example.com'),
			('` + plainUsr + `', 'plain@example.com')`,
		seed,
	}
	for _, stmt := range stmts {
		if stmt == "" {
			continue
		}
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	return ctx, NewStore(resources)
}

func roleOf(t *testing.T, store Store, userID string) string {
	t.Helper()
	role, ok := store.Lookup(userID)
	if !ok {
		return ""
	}
	return role
}

// Nobody may change their own role: self-promotion is the escalation this design
// exists to prevent, and self-demotion is a footgun.
func TestSetRoleRefusesSelfChange(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin'), ('`+adminB+`', 'admin')`)

	if err := store.SetRole(ctx, adminA, adminA, "editor"); err == nil {
		t.Fatal("expected a self-change to be refused")
	}
	if err := store.Revoke(ctx, adminA, adminA); err == nil {
		t.Fatal("expected self-revocation to be refused")
	}
	if role := roleOf(t, store, adminA); role != "admin" {
		t.Fatalf("role changed despite refusal: %q", role)
	}
}

// Demoting or removing the last admin would leave nobody able to grant access.
func TestLastAdminIsProtected(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin')`)

	if err := store.SetRole(ctx, plainUsr, adminA, "editor"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}
	if err := store.Revoke(ctx, plainUsr, adminA); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin on revoke, got %v", err)
	}
	if role := roleOf(t, store, adminA); role != "admin" {
		t.Fatalf("last admin lost their role: %q", role)
	}

	// With a second admin present, the first may be demoted.
	if err := store.SetRole(ctx, adminA, plainUsr, "admin"); err != nil {
		t.Fatalf("promote second admin: %v", err)
	}
	if err := store.SetRole(ctx, plainUsr, adminA, "editor"); err != nil {
		t.Fatalf("demotion should be allowed once another admin exists: %v", err)
	}
	if role := roleOf(t, store, adminA); role != "editor" {
		t.Fatalf("expected editor after demotion, got %q", role)
	}
}

// A grant is only meaningful for a user of this project.
func TestSetRoleRejectsUnknownUser(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin')`)

	if err := store.SetRole(ctx, adminA, stranger, "editor"); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("expected ErrUnknownUser, got %v", err)
	}
	if err := store.SetRole(ctx, adminA, "", "editor"); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("expected ErrUnknownUser for a blank id, got %v", err)
	}
}

func TestSetRoleUpsertsAndAudits(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin')`)

	if err := store.SetRole(ctx, adminA, plainUsr, "editor"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if role := roleOf(t, store, plainUsr); role != "editor" {
		t.Fatalf("expected editor, got %q", role)
	}

	// Second call updates in place rather than failing on the unique index.
	if err := store.SetRole(ctx, adminA, plainUsr, "developer"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if role := roleOf(t, store, plainUsr); role != "developer" {
		t.Fatalf("expected developer, got %q", role)
	}

	store.Audit(ctx, adminA, plainUsr, "set_role", "developer")

	pool, err := store.pool()
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM _supatype.studio_audit
		  WHERE actor_id = $1::uuid AND target_id = $2::uuid AND action = 'set_role'`,
		adminA, plainUsr).Scan(&count); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if count == 0 {
		t.Fatal("membership change was not audited")
	}
}

// An admin must be able to clear a stale cloud grant from self-host, where that
// account can never sign in.
func TestRevokeClearsEitherIdentity(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin'), ('`+adminB+`', 'admin')`)

	pool, err := store.pool()
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO _supatype.studio_members (platform_user_id, role) VALUES ($1::uuid, 'editor')`,
		cloudAcc); err != nil {
		t.Fatalf("seed cloud grant: %v", err)
	}

	if err := store.Revoke(ctx, adminA, cloudAcc); err != nil {
		t.Fatalf("revoke cloud grant: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM _supatype.studio_members WHERE platform_user_id = $1::uuid`,
		cloudAcc).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Fatal("cloud grant survived revocation")
	}
}

func TestListReportsBothIdentitySpaces(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin')`)

	pool, err := store.pool()
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO _supatype.studio_members (platform_user_id, role) VALUES ($1::uuid, 'editor')`,
		cloudAcc); err != nil {
		t.Fatalf("seed cloud grant: %v", err)
	}

	members, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d (%+v)", len(members), members)
	}

	// Project users first, and each row says which space it belongs to.
	if members[0].PlatformAccount || members[0].Email != "admin-a@example.com" {
		t.Fatalf("expected the project user first, got %+v", members[0])
	}
	if !members[1].PlatformAccount || members[1].Email != "" {
		t.Fatalf("expected a cloud grant with no project email, got %+v", members[1])
	}
}
