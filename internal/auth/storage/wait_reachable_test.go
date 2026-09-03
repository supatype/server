package storage

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// A database that is still starting must be waited out, not treated as fatal.
//
// This is the path migrations take. They build their own pop connection rather
// than going through DialContext, so nothing had contacted the database until
// the first query, and that query exits the process. On a host slow enough for
// Postgres to still be in recovery when the server boots, that lost the
// container every time.
func TestRetryTransientWaitsOutADatabaseThatIsStarting(t *testing.T) {
	attempts := 0
	err := retryTransient(t.Context(), func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("failed to connect to `user=x database=y`: the database system is in recovery mode")
		}
		return nil
	})

	if err != nil {
		t.Errorf("want the wait to succeed once the database answers, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("want 3 attempts, got %d", attempts)
	}
}

// A TLS refusal while Postgres is still coming up is a timing problem too. It
// is what the macOS runner produced, and treating it as final is what turned a
// slow start into a dead stack.
func TestRetryTransientWaitsOutATLSRefusalDuringStartup(t *testing.T) {
	attempts := 0
	err := retryTransient(t.Context(), func(context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("failed to connect to `user=x database=y`: 10.0.0.2:5432 (db): tls error: server refused TLS connection")
		}
		return nil
	})

	if err != nil {
		t.Errorf("want the wait to succeed on the retry, got %v", err)
	}
}

// Waiting must not bury a fact about the configuration. A wrong password does
// not become right, and looping on it hides the one message that says so.
func TestRetryTransientReturnsAFailureWaitingCannotFix(t *testing.T) {
	wrong := errors.New("pq: password authentication failed for user \"nope\"")
	attempts := 0

	err := retryTransient(t.Context(), func(context.Context) error {
		attempts++
		return wrong
	})

	if !errors.Is(err, wrong) {
		t.Errorf("want the original error back, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("want no retry on a permanent failure, got %d attempts", attempts)
	}
}

// Shutdown has to end the wait, or a stopping process hangs on a database that
// is never coming back.
func TestRetryTransientStopsWhenTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	transient := &net.OpError{Op: "dial", Err: errors.New("connection refused")}

	attempts := 0
	err := retryTransient(ctx, func(context.Context) error {
		attempts++
		if attempts == 2 {
			cancel()
		}
		return transient
	})

	if err == nil {
		t.Fatal("want an error once the context is cancelled")
	}
	if attempts > 3 {
		t.Errorf("want the wait to stop promptly after cancellation, got %d attempts", attempts)
	}
}

// WaitReachable has nothing to ping when the connection exposes no *sql.DB. It
// must not refuse to proceed on that account: the caller's first query then
// makes contact, which is exactly where things stood before.
func TestWaitReachableAcceptsAConnectionItCannotPing(t *testing.T) {
	if err := WaitReachable(t.Context(), nil); err != nil {
		t.Errorf("want no error for a connection with no pool, got %v", err)
	}
}

// The backoff has to be bounded, or a long outage ends up sleeping for hours
// between attempts and the recovery is invisible.
func TestBackoffIsCapped(t *testing.T) {
	if dialMaxDelay > 30*time.Second {
		t.Errorf("dialMaxDelay of %s is too long to notice a recovery", dialMaxDelay)
	}
	if dialFirstDelay > dialMaxDelay {
		t.Errorf("first delay %s exceeds the cap %s", dialFirstDelay, dialMaxDelay)
	}
}
