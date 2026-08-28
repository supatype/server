package studiomembers

import (
	"testing"

	"github.com/supatype/server/internal/dbpool"
)

// clearDSN removes any connection string a sibling test configured. The pool is
// process-global, so this has to be explicit rather than scoped by t.Setenv.
// Phase 3 replaces the global with an owned resource and this goes away.
func clearDSN(t *testing.T) {
	t.Helper()
	previous := dbpool.DSN()
	dbpool.Configure("")
	t.Cleanup(func() { dbpool.Configure(previous) })
}

// Which variable supplies the DSN is config.Config.SQLDSN's decision now. What
// this package promises is narrower: no DSN means Studio membership cannot be
// resolved, and it must say so rather than guess.
func TestAvailableFollowsDSN(t *testing.T) {
	clearDSN(t)
	if Available() {
		t.Fatal("expected unavailable with no DSN configured")
	}

	dbpool.Configure("postgres://user@localhost:5432/db")
	if !Available() {
		t.Fatal("expected available once a DSN is configured")
	}

	dbpool.Configure("")
	if Available() {
		t.Fatal("expected unavailable again once the DSN is cleared")
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
