package data

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/valkey"
)

func TestOpenWithNothingConfigured(t *testing.T) {
	r, err := Open(context.Background(), &config.Config{})
	if err != nil {
		t.Fatalf("a deployment with no cache and no database must still start: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if r.Cache() == nil {
		t.Fatal("Valkey must never be nil")
	}
	if r.Cache().Available() {
		t.Error("Valkey should report unavailable")
	}
	if r.HasDatabase() {
		t.Error("HasDatabase should be false")
	}
	if _, err := r.AdminPool(); !errors.Is(err, ErrNoDatabase) {
		t.Errorf("AdminPool: want ErrNoDatabase, got %v", err)
	}
}

// pgxpool parses the DSN eagerly but connects lazily, so a database that is
// merely down must not stop the process starting.
func TestOpenDoesNotRequireAReachableDatabase(t *testing.T) {
	r, err := Open(context.Background(), &config.Config{
		SQLDatabaseURL: "postgres://nobody:nothing@127.0.0.1:1/none",
	})
	if err != nil {
		t.Fatalf("an unreachable database must not fail Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if !r.HasDatabase() {
		t.Error("HasDatabase should be true once a DSN is configured")
	}
	if _, err := r.AdminPool(); err != nil {
		t.Errorf("AdminPool: %v", err)
	}
}

func TestOpenRejectsAnUnparseableDSN(t *testing.T) {
	if _, err := Open(context.Background(), &config.Config{SQLDatabaseURL: "://not a dsn"}); err == nil {
		t.Fatal("want an error for a DSN that cannot be parsed")
	}
}

// A configured but unreachable Valkey is fatal only in managed mode, where the
// tenant manifest lives in it. Elsewhere the caches degrade and the service runs.
func TestValkeyFailureIsFatalOnlyInManagedMode(t *testing.T) {
	const unreachable = "127.0.0.1:1"

	r, err := Open(context.Background(), &config.Config{Mode: "standalone", ValkeyAddr: unreachable})
	if err != nil {
		t.Fatalf("standalone must survive an unreachable Valkey: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if r.Cache().Available() {
		t.Error("an unreachable Valkey should leave the unavailable client in place")
	}

	if _, err := Open(context.Background(), &config.Config{Mode: "managed", ValkeyAddr: unreachable}); err == nil {
		t.Error("managed mode must refuse to start without the Valkey it needs")
	}
}

// Close runs from a defer that cannot know how far Open got, so it has to
// tolerate being called twice and on nothing at all.
func TestCloseIsSafeToRepeatAndOnNil(t *testing.T) {
	var nilResources *Resources
	if err := nilResources.Close(); err != nil {
		t.Errorf("Close on a nil Resources: %v", err)
	}

	r, err := Open(context.Background(), &config.Config{
		SQLDatabaseURL: "postgres://nobody:nothing@127.0.0.1:1/none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if r.HasDatabase() {
		t.Error("the pool should be gone after Close")
	}
	if _, err := r.AdminPool(); !errors.Is(err, ErrNoDatabase) {
		t.Errorf("AdminPool after Close: want ErrNoDatabase, got %v", err)
	}
}

// Teardown runs in reverse, so a resource is never closed before something that
// depends on it, and every failure is reported rather than just the first.
func TestCloseRunsInReverseAndReportsEveryFailure(t *testing.T) {
	var order []string
	r := &Resources{}
	r.onClose(func() error { order = append(order, "first"); return errors.New("first failed") })
	r.onClose(func() error { order = append(order, "second"); return nil })
	r.onClose(func() error { order = append(order, "third"); return errors.New("third failed") })

	err := r.Close()
	if got := strings.Join(order, ","); got != "third,second,first" {
		t.Errorf("teardown order = %s, want third,second,first", got)
	}
	if err == nil {
		t.Fatal("want the failures reported")
	}
	for _, want := range []string{"first failed", "third failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// fakeCache stands in for a connected Valkey so the path where a cache is
// acquired, registered for teardown and handed out can be exercised without one.
type fakeCache struct {
	valkey.Client
	closed int
}

func (f *fakeCache) Available() bool { return true }
func (f *fakeCache) Close()          { f.closed++ }

func TestOpenAcquiresAndClosesTheCache(t *testing.T) {
	cache := &fakeCache{Client: valkey.Unavailable()}
	original := openValkey
	openValkey = func(string) (valkey.Client, error) { return cache, nil }
	t.Cleanup(func() { openValkey = original })

	r, err := Open(context.Background(), &config.Config{ValkeyAddr: "valkey:6379"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Cache().Available() {
		t.Error("a connected cache should be reported available")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if cache.closed != 1 {
		t.Errorf("the cache should be closed exactly once, got %d", cache.closed)
	}
}

// A zero Resources must behave like one with nothing configured, since that is
// what a caller who built the struct directly will hand around.
func TestZeroResourcesYieldsTheUnavailableCache(t *testing.T) {
	if (&Resources{}).Cache().Available() {
		t.Error("an unset cache field must yield the unavailable client")
	}
	var nilResources *Resources
	if nilResources.Cache() == nil {
		t.Error("Cache on a nil Resources must still return a client")
	}
}

// closeAfter reports both the reason Open failed and any failure tearing down
// what it had already acquired, rather than losing one of them.
func TestCloseAfterReportsBothFailures(t *testing.T) {
	r := &Resources{}
	r.onClose(func() error { return errors.New("teardown failed") })

	err := r.closeAfter(errors.New("open failed"))
	for _, want := range []string{"open failed", "teardown failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// When teardown succeeds, only the original cause is reported.
func TestCloseAfterReportsOnlyTheCauseWhenTeardownSucceeds(t *testing.T) {
	r := &Resources{}
	r.onClose(func() error { return nil })

	err := r.closeAfter(errors.New("open failed"))
	if err.Error() != "open failed" {
		t.Errorf("got %q, want just the cause", err)
	}
}
