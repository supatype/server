package studiomembers

import "testing"

func clearDSN(t *testing.T) {
	t.Helper()
	t.Setenv("SUPATYPE_SQL_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")
}

func TestAvailableFollowsDSN(t *testing.T) {
	clearDSN(t)
	if Available() {
		t.Fatal("expected unavailable with no DSN configured")
	}

	t.Setenv("DATABASE_URL", "postgres://user@localhost:5432/db")
	if !Available() {
		t.Fatal("expected available once a DSN is configured")
	}

	t.Setenv("DATABASE_URL", "")
	t.Setenv("SUPATYPE_SQL_DATABASE_URL", "postgres://user@localhost:5432/db")
	if !Available() {
		t.Fatal("expected the Supatype-specific DSN to be honoured")
	}
}

// An admin UI that opens up when its authority is unreachable is worse than one
// that is briefly unavailable, so every failure to establish membership denies.
//
// This test permanently resolves the shared pool to "no DSN" for the rest of
// this test binary — deliberate, since nothing here should reach a database.
func TestLookupDeniesWithoutDatabase(t *testing.T) {
	clearDSN(t)

	if role, ok := Lookup("11111111-2222-3333-4444-555555555555"); ok || role != "" {
		t.Fatalf("expected denial with no database, got (%q, %v)", role, ok)
	}
}

func TestLookupDeniesEmptyUserID(t *testing.T) {
	if role, ok := Lookup("   "); ok || role != "" {
		t.Fatalf("expected denial for a blank user id, got (%q, %v)", role, ok)
	}
}
