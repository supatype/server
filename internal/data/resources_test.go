package data

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace"
)

func TestOpenWithNothingConfigured(t *testing.T) {
	r, err := Open(context.Background(), &config.Config{})
	if err != nil {
		t.Fatalf("a deployment with no cache and no database must still start: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	if r.Cache() == nil {
		t.Fatal("the keyspace client must never be nil")
	}
	if r.Cache().Available() {
		t.Error("the keyspace client should report unavailable")
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

// A configured but unreachable keyspace is fatal only in managed mode, where the
// tenant manifest lives in it. Elsewhere the caches degrade and the service runs.
func TestKeyspaceFailureIsFatalOnlyInManagedMode(t *testing.T) {
	const unreachable = "127.0.0.1:1"

	r, err := Open(context.Background(), &config.Config{Mode: "standalone", KeyspaceAddr: unreachable})
	if err != nil {
		t.Fatalf("standalone must survive an unreachable keyspace: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if r.Cache().Available() {
		t.Error("an unreachable keyspace should leave the unavailable client in place")
	}

	if _, err := Open(context.Background(), &config.Config{Mode: "managed", KeyspaceAddr: unreachable}); err == nil {
		t.Error("managed mode must refuse to start without the keyspace it needs")
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

// fakeCache stands in for a connected keyspace so the path where a cache is
// acquired, registered for teardown and handed out can be exercised without one.
type fakeCache struct {
	keyspace.Client
	closed int
}

func (f *fakeCache) Available() bool { return true }
func (f *fakeCache) Close()          { f.closed++ }

func TestOpenAcquiresAndClosesTheCache(t *testing.T) {
	cache := &fakeCache{Client: keyspace.Unavailable()}
	original := openKeyspace
	openKeyspace = func(string) (keyspace.Client, error) { return cache, nil }
	t.Cleanup(func() { openKeyspace = original })

	r, err := Open(context.Background(), &config.Config{KeyspaceAddr: "db:6379"})
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

// The branch is unreachable once ParseConfig has succeeded, so the seam is what
// proves it reports rather than handing back a nil pool for something to
// dereference later.
func TestOpenAdminPoolReportsAConstructorFailure(t *testing.T) {
	original := newPool
	t.Cleanup(func() { newPool = original })
	newPool = func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error) {
		return nil, errors.New("refused")
	}

	resources, err := Open(context.Background(), &config.Config{DatabaseURL: "postgres://user@localhost:5432/db"})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "admin pool") {
		t.Errorf("the error should say which pool: %v", err)
	}
	if resources != nil {
		t.Error("nothing should be handed back to close")
	}
}

// ─── the platform / project keyspace split ────────────────────────────────────

// One address means one client doing both jobs. Two clients against the same
// server would double the connections for nothing, and would make an ordinary
// single-keyspace deployment pay for a split it did not ask for.
func TestOpenSharesOneClientWhenBothAddressesMatch(t *testing.T) {
	var opened []string
	cache := &fakeCache{Client: keyspace.Unavailable()}
	original := openKeyspace
	openKeyspace = func(addr string) (keyspace.Client, error) {
		opened = append(opened, addr)
		return cache, nil
	}
	t.Cleanup(func() { openKeyspace = original })

	r, err := Open(context.Background(), &config.Config{
		KeyspaceAddr:        "db:6379",
		ProjectKeyspaceAddr: "db:6379",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 {
		t.Errorf("one address should open one client, got %d: %v", len(opened), opened)
	}
	if r.Cache() != r.PlatformCache() {
		t.Error("both halves should be the same client when the addresses match")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if cache.closed != 1 {
		t.Errorf("the shared client should be closed once, got %d", cache.closed)
	}
}

// Two addresses mean two clients, and each half must get the right one.
func TestOpenSeparatesTheProjectKeyspaceFromThePlatformOne(t *testing.T) {
	platform := &fakeCache{Client: keyspace.Unavailable()}
	project := &fakeCache{Client: keyspace.Unavailable()}
	original := openKeyspace
	openKeyspace = func(addr string) (keyspace.Client, error) {
		if addr == "postgres-platform:6379" {
			return platform, nil
		}
		return project, nil
	}
	t.Cleanup(func() { openKeyspace = original })

	r, err := Open(context.Background(), &config.Config{
		KeyspaceAddr:        "postgres-platform:6379",
		ProjectKeyspaceAddr: "postgres:6379",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.PlatformCache() != keyspace.Client(platform) {
		t.Error("PlatformCache should be the client for the platform address")
	}
	if r.Cache() != keyspace.Client(project) {
		t.Error("Cache should be the client for the project address")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if platform.closed != 1 || project.closed != 1 {
		t.Errorf("both clients should be closed once, got platform=%d project=%d",
			platform.closed, project.closed)
	}
}

// The project keyspace usually lives in the project's own Postgres, which may
// still be starting when this pod is. Making it fatal would turn a cold start
// into a crashloop and take the gateway down to protect a cache.
func TestOpenSurvivesAnUnreachableProjectKeyspaceInManagedMode(t *testing.T) {
	platform := &fakeCache{Client: keyspace.Unavailable()}
	original := openKeyspace
	openKeyspace = func(addr string) (keyspace.Client, error) {
		if addr == "postgres-platform:6379" {
			return platform, nil
		}
		return nil, errors.New("connection refused")
	}
	t.Cleanup(func() { openKeyspace = original })

	r, err := Open(context.Background(), &config.Config{
		Mode:                "managed",
		KeyspaceAddr:        "postgres-platform:6379",
		ProjectKeyspaceAddr: "postgres:6379",
	})
	if err != nil {
		t.Fatalf("an unreachable project keyspace must not fail Open: %v", err)
	}
	if r.Cache().Available() {
		t.Error("the response cache should report unavailable so requests bypass it")
	}
	if !r.PlatformCache().Available() {
		t.Error("the platform keyspace was reachable and must stay available")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

// The platform keyspace is the opposite case, and that has not changed: the
// route manifest lives in it, so a managed pod that cannot read it cannot serve.
func TestOpenStillFailsOnAnUnreachablePlatformKeyspaceInManagedMode(t *testing.T) {
	original := openKeyspace
	openKeyspace = func(string) (keyspace.Client, error) { return nil, errors.New("connection refused") }
	t.Cleanup(func() { openKeyspace = original })

	_, err := Open(context.Background(), &config.Config{
		Mode:                "managed",
		KeyspaceAddr:        "postgres-platform:6379",
		ProjectKeyspaceAddr: "postgres:6379",
	})
	if err == nil {
		t.Fatal("an unreachable platform keyspace in managed mode must fail Open")
	}
}

// A zero Resources must hand out a usable platform client for the same reason it
// hands out a usable cache: a caller that built the struct directly still calls it.
func TestZeroResourcesYieldsTheUnavailablePlatformCache(t *testing.T) {
	if (&Resources{}).PlatformCache().Available() {
		t.Error("an unset platform field must yield the unavailable client")
	}
	var nilResources *Resources
	if nilResources.PlatformCache() == nil {
		t.Error("PlatformCache on a nil Resources must still return a client")
	}
}

// Close resets both halves, so a Resources reused after teardown cannot hand out
// a client whose connection is gone.
func TestCloseResetsBothKeyspaces(t *testing.T) {
	cache := &fakeCache{Client: keyspace.Unavailable()}
	original := openKeyspace
	openKeyspace = func(string) (keyspace.Client, error) { return cache, nil }
	t.Cleanup(func() { openKeyspace = original })

	r, err := Open(context.Background(), &config.Config{KeyspaceAddr: "db:6379"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if r.Cache().Available() || r.PlatformCache().Available() {
		t.Error("both halves should be unavailable after Close")
	}
}
