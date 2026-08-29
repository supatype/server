package studiomembers

import (
	"context"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
)

// storeFor builds a Store over the given DSN. An empty DSN yields a Store with
// no database, which every method must handle by denying rather than panicking.
func storeFor(t *testing.T, dsn string) Store {
	t.Helper()
	resources, err := data.Open(context.Background(), &config.Config{SQLDatabaseURL: dsn})
	if err != nil {
		t.Fatalf("open resources: %v", err)
	}
	t.Cleanup(func() { _ = resources.Close() })
	return NewStore(resources)
}

// Which variable supplies the DSN is config.Config.SQLDSN's decision. What this
// package promises is narrower: no database means Studio membership cannot be
// resolved, and it must say so rather than guess.
func TestAvailableFollowsTheConfiguredDatabase(t *testing.T) {
	if storeFor(t, "").Available() {
		t.Error("expected unavailable with no database configured")
	}
	if !storeFor(t, "postgres://user@localhost:5432/db").Available() {
		t.Error("expected available once a DSN is configured")
	}
}

// A Store with no resources at all must behave the same as one with no DSN,
// because that is what a zero value looks like.
func TestZeroStoreDeniesRatherThanPanics(t *testing.T) {
	var s Store
	if s.Available() {
		t.Error("a zero Store must not claim to be available")
	}
	if _, ok := s.Lookup("11111111-2222-3333-4444-555555555555"); ok {
		t.Error("a zero Store must deny lookups")
	}
}

// An admin UI that opens up when its authority is unreachable is worse than one
// that is briefly unavailable, so every failure to establish membership denies.
//
// The Store is built per test now, so this no longer has to poison a shared
// singleton for the rest of the binary to make its point.
func TestLookupDeniesWithoutDatabase(t *testing.T) {
	if role, ok := storeFor(t, "").Lookup("11111111-2222-3333-4444-555555555555"); ok || role != "" {
		t.Fatalf("expected denial with no database, got (%q, %v)", role, ok)
	}
}

func TestLookupDeniesEmptyUserID(t *testing.T) {
	if role, ok := storeFor(t, "").Lookup("   "); ok || role != "" {
		t.Fatalf("expected denial for a blank user id, got (%q, %v)", role, ok)
	}
}
