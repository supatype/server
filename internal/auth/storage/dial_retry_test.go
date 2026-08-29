package storage

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
)

// The line between "not there yet" and "wrong".
//
// Too broad and a wrong password becomes an infinite wait with nothing surfaced; too narrow and the
// server exits on a cold start, which is what it used to do — hidden by the Compose `db` healthcheck
// that a stack pointed at an external database does not have.

func TestTransientDialErrorRetriesConnectionFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"pgx class 08", &pgconn.PgError{Code: "08006", Message: "connection failure"}},
		{"pgx cannot connect now", &pgconn.PgError{Code: "57P03", Message: "starting up"}},
		{"pgx too many connections", &pgconn.PgError{Code: "53300"}},
		{"pq class 08", &pq.Error{Code: "08001"}},
		{"net op error", &net.OpError{Op: "dial", Err: errors.New("connection refused")}},
		{"dns error", &net.DNSError{Err: "no such host", Name: "db"}},
		{"wrapped refusal", fmt.Errorf("opening database connection: %w", errors.New("dial tcp 10.0.0.5:5432: connect: connection refused"))},
		{"starting up message", errors.New("pq: the database system is starting up")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !isTransientDialError(tc.err) {
				t.Fatalf("expected a retryable error, got a fatal one: %v", tc.err)
			}
		})
	}
}

func TestTransientDialErrorDoesNotRetryConfigurationFailures(t *testing.T) {
	// Retrying any of these forever would bury the one message that says what to fix.
	cases := []struct {
		name string
		err  error
	}{
		{"invalid password", &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}},
		{"invalid authorization", &pgconn.PgError{Code: "28000"}},
		{"missing database", &pgconn.PgError{Code: "3D000", Message: `database "supatype" does not exist`}},
		{"insufficient privilege", &pgconn.PgError{Code: "42501"}},
		{"pq bad password", &pq.Error{Code: "28P01"}},
		{"unparseable dsn", errors.New("cannot parse `postgres://`: invalid dsn")},
		{"nil", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isTransientDialError(tc.err) {
				t.Fatalf("expected a fatal error, got a retryable one: %v", tc.err)
			}
		})
	}
}

func TestDialWithRetrySucceedsAfterTransientFailures(t *testing.T) {
	attempts := 0
	conn, err := DialWithRetry(context.Background(), func(context.Context) (*Connection, error) {
		attempts++
		if attempts < 3 {
			return nil, &pgconn.PgError{Code: "08006", Message: "connection failure"}
		}
		return &Connection{}, nil
	})
	if err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}
	if conn == nil {
		t.Fatal("expected a connection")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestDialWithRetryReturnsFatalErrorImmediately(t *testing.T) {
	attempts := 0
	fatal := &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}
	_, err := DialWithRetry(context.Background(), func(context.Context) (*Connection, error) {
		attempts++
		return nil, fatal
	})
	if !errors.Is(err, fatal) {
		t.Fatalf("expected the original error back, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("a configuration error must not be retried; attempts = %d", attempts)
	}
}

func TestDialWithRetryStopsOnContextCancellation(t *testing.T) {
	// Shutdown mid-retry must not leave the loop running against a server that is going away.
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	done := make(chan error, 1)
	go func() {
		_, err := DialWithRetry(ctx, func(context.Context) (*Connection, error) {
			attempts++
			if attempts == 2 {
				cancel()
			}
			return nil, &pgconn.PgError{Code: "08006"}
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the last error to be returned after cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DialWithRetry ignored context cancellation")
	}
}
