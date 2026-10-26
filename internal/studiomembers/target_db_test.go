package studiomembers

import (
	"slices"
	"testing"
)

// The guard on admin accounts (studioauth/admin_target.go) finds whose
// account a request names by user id, session id or email; each must find
// the admin's Studio role, and a user with none must yield none.
func TestTargetRolesFindsTheAdminByIdSessionAndEmail(t *testing.T) {
	ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
		VALUES ('`+adminA+`', 'admin')`)
	pool, err := store.pool()
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	const session = "cccccccc-0000-4000-8000-000000000001"
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS auth.sessions`,
		`CREATE TABLE auth.sessions (id UUID PRIMARY KEY, user_id UUID NOT NULL)`,
		`INSERT INTO auth.sessions VALUES ('` + session + `', '` + adminA + `')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS auth.sessions`) })

	for _, c := range []struct {
		kind  TargetKind
		value string
		want  []string
	}{
		{TargetUserID, adminA, []string{"admin"}},
		{TargetSessionID, session, []string{"admin"}},
		{TargetEmail, " Admin-A@Example.com ", []string{"admin"}},
		{TargetUserID, plainUsr, nil},
		{TargetEmail, "plain@example.com", nil},
	} {
		got, err := store.TargetRoles(ctx, c.kind, c.value)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("TargetRoles(%d, %q) = %v, %v; want %v", c.kind, c.value, got, err, c.want)
		}
	}
}

// With no database the answer is unknown, which is an error, never "nobody".
func TestTargetRolesWithoutADatabaseIsAnError(t *testing.T) {
	if _, err := (Store{}).TargetRoles(t.Context(), TargetUserID, adminA); err == nil {
		t.Fatal("expected an error with no database")
	}
}

// A membership table the lookup cannot read, missing or of a shape it cannot
// scan, is an unknown answer: an error, never "this user holds no role".
func TestTargetRolesReportsATableItCannotRead(t *testing.T) {
	for _, tc := range []struct{ name, breakIt string }{
		{"missing", `DROP TABLE _supatype.studio_members`},
		{"unscannable role", `ALTER TABLE _supatype.studio_members ALTER COLUMN role TYPE text[] USING ARRAY[role]`},
	} {
		ctx, store := setupMembership(t, `INSERT INTO _supatype.studio_members (user_id, role)
			VALUES ('`+adminA+`', 'admin')`)
		if _, err := adminPool(t, store).Exec(ctx, tc.breakIt); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if roles, err := store.TargetRoles(ctx, TargetUserID, adminA); err == nil || roles != nil {
			t.Errorf("%s: got %v, %v; want an error and no roles", tc.name, roles, err)
		}
	}
}
