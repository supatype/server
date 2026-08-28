package apiconfig

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// A store that cannot be read must not answer with a config the caller will
// then act on as though it were the tenant's. Every failing path returns the
// defaults alongside the error, so a caller that ignores the error still gets a
// safe shape rather than a zero one, and one that checks it can say why.

func TestDefaultApiConfig(t *testing.T) {
	got := DefaultApiConfig()

	if got.Rest.Schema != "public" {
		t.Errorf("rest schema = %q", got.Rest.Schema)
	}
	if got.Rest.MaxRows != 1000 {
		t.Errorf("rest max rows = %d", got.Rest.MaxRows)
	}
	// Zero means no ceiling is imposed here, which is the out-of-the-box state:
	// caching is off per table until a table opts in.
	if got.Rest.CacheMaxTTL != 0 {
		t.Errorf("cache max ttl = %d", got.Rest.CacheMaxTTL)
	}
	if got.Rest.CacheTables == nil {
		t.Error("cache tables should be an empty map, not nil, so a caller can write to it")
	}
	if len(got.Rest.CacheTables) != 0 {
		t.Errorf("no table should be cached by default, got %v", got.Rest.CacheTables)
	}
	if !got.GraphQL.Introspection {
		t.Error("introspection should be on by default")
	}
	if got.GraphQL.MaxQueryDepth != 10 || got.GraphQL.MaxRows != 1000 {
		t.Errorf("graphql defaults = %+v", got.GraphQL)
	}
}

// The defaults are handed out by value, so one caller mutating what it got
// cannot change what the next caller receives.
func TestDefaultApiConfigIsNotShared(t *testing.T) {
	first := DefaultApiConfig()
	first.Rest.CacheTables["users"] = RestTableCacheConfig{Enabled: true}
	first.Rest.MaxRows = 1

	second := DefaultApiConfig()
	if len(second.Rest.CacheTables) != 0 {
		t.Errorf("the default cache table map is shared: %v", second.Rest.CacheTables)
	}
	if second.Rest.MaxRows != 1000 {
		t.Errorf("max rows = %d", second.Rest.MaxRows)
	}
}

func TestTableCacheAllowed(t *testing.T) {
	enabled := RestTableCacheConfig{Enabled: true, AllowPublic: true}
	configured := RestConfig{CacheTables: map[string]RestTableCacheConfig{
		"posts":   enabled,
		"secrets": {Enabled: false, AllowPublic: true},
	}}

	for name, tc := range map[string]struct {
		cfg   RestConfig
		table string
		want  RestTableCacheConfig
		ok    bool
	}{
		"a table that opted in":    {configured, "posts", enabled, true},
		"a table that opted out":   {configured, "secrets", RestTableCacheConfig{}, false},
		"a table nobody mentioned": {configured, "orders", RestTableCacheConfig{}, false},
		"no tables configured":     {RestConfig{}, "posts", RestTableCacheConfig{}, false},
		"an empty map":             {RestConfig{CacheTables: map[string]RestTableCacheConfig{}}, "posts", RestTableCacheConfig{}, false},
		"no table name":            {configured, "", RestTableCacheConfig{}, false},
		"no table name, no tables": {RestConfig{}, "", RestTableCacheConfig{}, false},
	} {
		got, ok := tc.cfg.TableCacheAllowed(tc.table)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got (%+v, %v), want (%+v, %v)", name, got, ok, tc.want, tc.ok)
		}
	}
}

// A disabled entry must not leak its AllowPublic flag to the caller, which
// would otherwise read as permission to serve a cached row to an anonymous
// request.
func TestTableCacheAllowedReturnsNothingWhenDisabled(t *testing.T) {
	cfg := RestConfig{CacheTables: map[string]RestTableCacheConfig{
		"secrets": {Enabled: false, AllowPublic: true},
	}}
	got, ok := cfg.TableCacheAllowed("secrets")
	if ok {
		t.Fatal("a disabled table must not be allowed")
	}
	if got.AllowPublic {
		t.Error("the AllowPublic flag of a disabled table leaked to the caller")
	}
}

func storePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "api-config.json")
}

// A project that has never configured anything has no file, and that is not an
// error: it is the default configuration.
func TestGetOnAMissingFileReturnsTheDefaults(t *testing.T) {
	cfg, err := NewFileStore(storePath(t)).Get(context.Background())
	if err != nil {
		t.Fatalf("a missing file is not an error: %v", err)
	}
	if cfg.Rest.Schema != "public" || cfg.GraphQL.MaxQueryDepth != 10 {
		t.Errorf("config = %+v, want the defaults", cfg)
	}
}

func TestSetThenGetRoundTrips(t *testing.T) {
	store := NewFileStore(storePath(t))
	want := ApiConfig{
		Rest: RestConfig{
			Schema:      "api",
			MaxRows:     50,
			CacheMaxTTL: 30,
			CacheTables: map[string]RestTableCacheConfig{"posts": {Enabled: true, AllowPublic: true}},
		},
		GraphQL: GraphQLConfig{Introspection: false, MaxQueryDepth: 3, MaxRows: 25},
	}

	if err := store.Set(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Rest.Schema != want.Rest.Schema || got.Rest.MaxRows != want.Rest.MaxRows ||
		got.Rest.CacheMaxTTL != want.Rest.CacheMaxTTL || got.GraphQL != want.GraphQL {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got.Rest.CacheTables["posts"] != want.Rest.CacheTables["posts"] {
		t.Errorf("cache tables = %v", got.Rest.CacheTables)
	}
}

// Written for a human to read and edit, and never world-readable: the file can
// name the tables whose rows may be served from cache to anonymous callers.
func TestSetWritesIndentedJSONThatOnlyTheOwnerCanRead(t *testing.T) {
	path := storePath(t)
	if err := NewFileStore(path).Set(context.Background(), DefaultApiConfig()); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path) // #nosec G304 -- this test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("not valid JSON:\n%s", raw)
	}
	if !strings.Contains(string(raw), "\n  \"rest\": {") {
		t.Errorf("expected indented JSON, got:\n%s", raw)
	}

	// Windows has no Unix mode bits and reports 0666 whatever was asked for, so
	// the permission is only assertable where it is actually applied. CI is
	// Linux, which is where it matters.
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("mode = %v, want owner-only", perm)
	}
}

// A half-written or hand-mangled file must be reported. Serving the defaults
// silently would present as a tenant's configuration having been forgotten.
func TestGetReportsUnreadableJSON(t *testing.T) {
	path := storePath(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewFileStore(path).Get(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	if cfg.Rest.Schema != "public" {
		t.Errorf("the defaults should come back alongside the error, got %+v", cfg)
	}
}

// Any read failure other than "no such file" is a real one: the file exists and
// cannot be read, which is not the same as never having been configured.
func TestGetReportsAReadFailureThatIsNotAbsence(t *testing.T) {
	dir := t.TempDir()
	cfg, err := NewFileStore(dir).Get(context.Background())
	if err == nil {
		t.Fatal("reading a directory as the config file must be reported")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("this is not absence: %v", err)
	}
	if cfg.Rest.Schema != "public" {
		t.Errorf("the defaults should come back alongside the error, got %+v", cfg)
	}
}

func TestSetReportsAWriteFailure(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "no-such-dir", "api-config.json"))
	if err := store.Set(context.Background(), DefaultApiConfig()); err == nil {
		t.Fatal("writing into a directory that does not exist must be reported")
	}
}

// The branch is unreachable with a real ApiConfig, so the seam proves it
// reports rather than writing a truncated file.
func TestSetReportsAMarshalFailure(t *testing.T) {
	path := storePath(t)
	original := marshalConfig
	t.Cleanup(func() { marshalConfig = original })
	marshalConfig = func(ApiConfig) ([]byte, error) { return nil, errors.New("nope") }

	if err := NewFileStore(path).Set(context.Background(), DefaultApiConfig()); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("nothing should have been written")
	}
}

// The mutex is the point of the type: the admin API serves reads while a write
// is in flight.
func TestFileStoreIsSafeUnderConcurrency(t *testing.T) {
	store := NewFileStore(storePath(t))
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = store.Set(ctx, DefaultApiConfig()) }()
		go func() { defer wg.Done(); _, _ = store.Get(ctx) }()
	}
	wg.Wait()
}

// FileStore is what the admin API is handed, so it has to satisfy the interface.
var _ Store = (*FileStore)(nil)
