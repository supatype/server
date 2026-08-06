package dbpool

import (
	"context"
	"errors"
	"testing"
)

func TestDSNPrefersSupatypeVariable(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app@localhost:5432/db")
	t.Setenv("SUPATYPE_SQL_DATABASE_URL", "postgres://admin@localhost:5432/db")

	if got := DSN(); got != "postgres://admin@localhost:5432/db" {
		t.Fatalf("expected the Supatype-specific DSN to win, got %q", got)
	}

	t.Setenv("SUPATYPE_SQL_DATABASE_URL", "")
	if got := DSN(); got != "postgres://app@localhost:5432/db" {
		t.Fatalf("expected fallback to DATABASE_URL, got %q", got)
	}
}

// Features built on this pool are optional, so a deployment with no DSN must get
// a plain error to fail closed on rather than a nil pool to panic on.
func TestPoolReportsMissingDSN(t *testing.T) {
	t.Setenv("SUPATYPE_SQL_DATABASE_URL", "")
	t.Setenv("DATABASE_URL", "")

	pool, err := Pool(context.Background())
	if !errors.Is(err, ErrNoDSN) {
		t.Fatalf("expected ErrNoDSN, got %v", err)
	}
	if pool != nil {
		t.Fatal("expected no pool when no DSN is configured")
	}
}
