package valkey_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/supatype/server/internal/data/valkey"
	"github.com/supatype/server/internal/data/valkey/valkeytest"
	"github.com/supatype/server/internal/proxy"
)

// The managed merge is what decides where a tenant's requests go, so what wins
// over what is the whole of its behaviour. An external test package, because
// the in-memory client it uses imports this one.

const ref = "proj-1"

// fileManifest is the layer a deployment ships on disk.
func fileManifest() *proxy.RouteManifest {
	return &proxy.RouteManifest{
		Schema:       "public",
		PostgRESTURL: "http://file-rest",
		StorageURL:   "http://file-store",
	}
}

// storing returns a cache holding this tenant config and manifest override.
func storing(t *testing.T, cfg *valkey.TenantConfig, override *proxy.RouteManifest) *valkeytest.Client {
	t.Helper()

	cache := valkeytest.New()
	if cfg != nil {
		cache.WithTenant(ref, cfg)
	}
	if override != nil {
		raw, err := json.Marshal(override)
		if err != nil {
			t.Fatal(err)
		}
		cache.Put(valkey.RouteManifestKey(ref), raw, 60)
	}
	return cache
}

func TestRouteManifestKey(t *testing.T) {
	if got := valkey.RouteManifestKey("proj-1"); got != "tenant:proj-1:manifest" {
		t.Errorf("key = %q", got)
	}
}

// Nothing in the cache leaves the file manifest as it was.
func TestMergeWithNothingStored(t *testing.T) {
	got, err := valkey.LoadMergedManagedManifest(context.Background(), valkeytest.New(), ref, fileManifest())
	if err != nil {
		t.Fatal(err)
	}
	if got.PostgRESTURL != "http://file-rest" || got.Schema != "public" {
		t.Errorf("manifest = %+v", got)
	}
}

// The layering: the file is the base, the tenant config overrides it, and the
// tenant's own manifest overrides that. Getting the order wrong sends a
// tenant's requests at another tenant's upstream.
func TestTheLayeringOrder(t *testing.T) {
	cfg := &valkey.TenantConfig{PostgRESTURL: "http://config-rest", Schema: "config"}
	override := &proxy.RouteManifest{PostgRESTURL: "http://override-rest"}

	got, err := valkey.LoadMergedManagedManifest(context.Background(), storing(t, cfg, override), ref, fileManifest())
	if err != nil {
		t.Fatal(err)
	}
	if got.PostgRESTURL != "http://override-rest" {
		t.Errorf("postgrest = %q, want the tenant manifest to win", got.PostgRESTURL)
	}
	if got.Schema != "config" {
		t.Errorf("schema = %q, want the tenant config to win over the file", got.Schema)
	}
	if got.StorageURL != "http://file-store" {
		t.Errorf("storage = %q, want the file's value where nothing overrode it", got.StorageURL)
	}
}

// The merge reads the tenant's config and its manifest, so a failure reading
// either is a failure to route: guessing would send requests somewhere.
func TestMergeReportsWhatItCannotRead(t *testing.T) {
	badConfig := valkeytest.New()
	badConfig.TenantErr = valkeytest.ErrFailed

	badManifest := valkeytest.New()
	badManifest.GetErr = valkeytest.ErrFailed

	for name, cache := range map[string]*valkeytest.Client{
		"the tenant config": badConfig,
		"the manifest":      badManifest,
	} {
		if _, err := valkey.LoadMergedManagedManifest(context.Background(), cache, ref, fileManifest()); err == nil {
			t.Errorf("%s could not be read and no error came back", name)
		}
	}
}

// A manifest override that will not parse is reported rather than ignored: a
// tenant that pushed a broken manifest should hear about it, not silently get
// the previous routing.
func TestAnOverrideThatWillNotParse(t *testing.T) {
	cache := valkeytest.New()
	cache.Put(valkey.RouteManifestKey(ref), []byte("{not json"), 60)

	if _, err := valkey.LoadMergedManagedManifest(context.Background(), cache, ref, fileManifest()); err == nil {
		t.Error("want an error")
	}
}

// ─── The per-tenant cache ─────────────────────────────────────────────────────

// The second request for a tenant is answered from memory, which is the point:
// otherwise every request costs a round trip to Valkey.
func TestTheCacheServesRepeatsFromMemory(t *testing.T) {
	cache := storing(t, &valkey.TenantConfig{Schema: "app"}, nil)
	c := valkey.NewTenantManifestCache(cache, time.Minute, fileManifest)

	for i := 0; i < 5; i++ {
		got, err := c.Get(context.Background(), ref)
		if err != nil {
			t.Fatal(err)
		}
		if got.Schema != "app" {
			t.Fatalf("schema = %q", got.Schema)
		}
	}
	// One GetTenantConfig and one GetBytes, once.
	if cache.Gets != 1 {
		t.Errorf("the manifest key was fetched %d times, want 1", cache.Gets)
	}
}

// Each caller gets its own copy, so one request mutating what it was given
// cannot change what the next request sees.
func TestTheCacheHandsOutCopies(t *testing.T) {
	c := valkey.NewTenantManifestCache(storing(t, &valkey.TenantConfig{Schema: "app"}, nil), time.Minute, fileManifest)
	ctx := context.Background()

	first, err := c.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	first.Schema = "mutated"
	first.PostgRESTURL = "http://mutated"

	second, err := c.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if second.Schema != "app" || second.PostgRESTURL != "http://file-rest" {
		t.Errorf("the cached manifest was mutated by a caller: %+v", second)
	}
}

// An expired entry is refetched, so a tenant that changes its routing is not
// stuck with the old one for ever.
func TestTheCacheExpires(t *testing.T) {
	cache := storing(t, &valkey.TenantConfig{Schema: "app"}, nil)
	c := valkey.NewTenantManifestCache(cache, time.Nanosecond, fileManifest)
	ctx := context.Background()

	if _, err := c.Get(ctx, ref); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := c.Get(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if cache.Gets < 2 {
		t.Errorf("fetched %d times, want the entry to have expired", cache.Gets)
	}
}

// Flushing is what a manifest file change triggers, so the next request has to
// go back to Valkey.
func TestFlushDropsEverything(t *testing.T) {
	cache := storing(t, &valkey.TenantConfig{Schema: "app"}, nil)
	c := valkey.NewTenantManifestCache(cache, time.Hour, fileManifest)
	ctx := context.Background()

	if _, err := c.Get(ctx, ref); err != nil {
		t.Fatal(err)
	}
	c.Flush()
	if _, err := c.Get(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if cache.Gets != 2 {
		t.Errorf("fetched %d times, want the flush to have dropped the entry", cache.Gets)
	}
}

// Flushing nothing is a no-op rather than a panic: the cache is nil in every
// mode but managed.
func TestFlushOnNoCache(t *testing.T) {
	var c *valkey.TenantManifestCache
	c.Flush()
}

// A request with no tenant gets the file manifest, which is the single-tenant
// case, and it gets a copy of it.
func TestNoTenantGetsTheFileManifest(t *testing.T) {
	c := valkey.NewTenantManifestCache(valkeytest.New(), time.Minute, fileManifest)

	got, err := c.Get(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.PostgRESTURL != "http://file-rest" {
		t.Errorf("manifest = %+v", got)
	}
	got.Schema = "mutated"
	if fileManifest().Schema == "mutated" {
		t.Error("the caller was handed the base rather than a copy")
	}
}

// A ttl of zero or less is a mistake, not a request for no caching, so it takes
// the default rather than fetching on every request.
func TestATTLOfZeroTakesTheDefault(t *testing.T) {
	cache := storing(t, &valkey.TenantConfig{Schema: "app"}, nil)
	c := valkey.NewTenantManifestCache(cache, 0, fileManifest)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := c.Get(ctx, ref); err != nil {
			t.Fatal(err)
		}
	}
	if cache.Gets != 1 {
		t.Errorf("fetched %d times, want the default TTL to have held", cache.Gets)
	}
}

// A failure is not cached, so the next request tries again rather than serving
// an error for the whole TTL.
func TestAFailureIsNotCached(t *testing.T) {
	cache := valkeytest.New()
	cache.TenantErr = valkeytest.ErrFailed
	c := valkey.NewTenantManifestCache(cache, time.Hour, fileManifest)
	ctx := context.Background()

	if _, err := c.Get(ctx, ref); !errors.Is(err, valkeytest.ErrFailed) {
		t.Fatalf("err = %v", err)
	}

	cache.TenantErr = nil
	cache.WithTenant(ref, &valkey.TenantConfig{Schema: "app"})
	got, err := c.Get(ctx, ref)
	if err != nil {
		t.Fatalf("the failure was cached: %v", err)
	}
	if got.Schema != "app" {
		t.Errorf("schema = %q", got.Schema)
	}
}

// Concurrent requests for the same tenant collapse into one fetch, which is
// what stops a cold cache stampeding Valkey on a busy pod.
func TestConcurrentRequestsCollapseIntoOneFetch(t *testing.T) {
	cache := storing(t, &valkey.TenantConfig{Schema: "app"}, nil)
	c := valkey.NewTenantManifestCache(cache, time.Hour, fileManifest)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Get(context.Background(), ref); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if cache.Gets > 4 {
		t.Errorf("fetched %d times for one tenant, want the requests to have collapsed", cache.Gets)
	}
}
