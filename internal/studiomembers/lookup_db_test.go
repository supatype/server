package studiomembers

import (
	"context"
	"os"
	"testing"

	"github.com/supatype/server/internal/dbpool"
)

// End-to-end membership resolution against a real Postgres.
//
// Skipped unless SUPATYPE_TEST_DSN points at a throwaway database — this test
// creates and drops `_supatype.studio_members`.
func TestLookupAgainstRealDatabase(t *testing.T) {
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run membership lookups against Postgres")
	}
	t.Setenv("SUPATYPE_SQL_DATABASE_URL", dsn)

	ctx := context.Background()
	pool, err := dbpool.Pool(ctx)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}

	const member = "11111111-2222-3333-4444-555555555555"
	const stranger = "99999999-8888-7777-6666-555555555555"

	for _, stmt := range []string{
		`CREATE SCHEMA IF NOT EXISTS _supatype`,
		`DROP TABLE IF EXISTS _supatype.studio_members`,
		`CREATE TABLE _supatype.studio_members (
			user_id UUID PRIMARY KEY,
			role TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`INSERT INTO _supatype.studio_members (user_id, role) VALUES ('` + member + `', 'admin')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS _supatype.studio_members`)
	})

	role, ok := Lookup(member)
	if !ok || role != "admin" {
		t.Fatalf("expected (admin, true) for a member, got (%q, %v)", role, ok)
	}

	if role, ok := Lookup(stranger); ok || role != "" {
		t.Fatalf("expected denial for a non-member, got (%q, %v)", role, ok)
	}

	// A malformed id must deny rather than error out of the request path.
	if role, ok := Lookup("not-a-uuid"); ok || role != "" {
		t.Fatalf("expected denial for a malformed id, got (%q, %v)", role, ok)
	}

	// A dropped table is an unreachable authority, not an open door.
	if _, err := pool.Exec(ctx, `DROP TABLE _supatype.studio_members`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if role, ok := Lookup(member); ok || role != "" {
		t.Fatalf("expected denial when the table is missing, got (%q, %v)", role, ok)
	}
}
