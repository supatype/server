package dbpool

import (
	"context"
	"errors"
	"testing"
)

func TestConfigureAndDSN(t *testing.T) {
	t.Cleanup(func() { Configure("") })

	Configure("postgres://admin@localhost:5432/db")
	if got := DSN(); got != "postgres://admin@localhost:5432/db" {
		t.Fatalf("DSN() = %q", got)
	}

	Configure("")
	if got := DSN(); got != "" {
		t.Fatalf("DSN() = %q, want empty after clearing", got)
	}
}

// The package no longer reads the environment. Which variable wins is the
// caller's decision, expressed once in config.Config.SQLDSN.
func TestDSNIgnoresTheEnvironment(t *testing.T) {
	t.Cleanup(func() { Configure("") })
	t.Setenv("SUPATYPE_SQL_DATABASE_URL", "postgres://from-env/db")
	t.Setenv("DATABASE_URL", "postgres://also-from-env/db")

	Configure("")
	if got := DSN(); got != "" {
		t.Fatalf("DSN() = %q; the environment must not reach this package", got)
	}
}

// Features built on this pool are optional, so a deployment with no DSN must get
// a plain error to fail closed on rather than a nil pool to panic on.
func TestPoolReportsMissingDSN(t *testing.T) {
	t.Cleanup(func() { Configure("") })
	Configure("")

	pool, err := Pool(context.Background())
	if !errors.Is(err, ErrNoDSN) {
		t.Fatalf("expected ErrNoDSN, got %v", err)
	}
	if pool != nil {
		t.Fatal("expected no pool when no DSN is configured")
	}
}
