package restcache

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/valkey"
	"github.com/supatype/server/internal/data/valkey/valkeytest"
)

// The cache had no test that ran a request through it. What it does with one is
// the whole of its behaviour: which requests it will keep, which it refuses to
// share between callers, and what it tells the caller it did.

const (
	cachedTable  = "posts"
	upstreamBody = `[{"id":1}]`
)

// upstream is the handler the cache sits in front of, counting how often it is
// actually reached.
type upstream struct {
	calls   int
	status  int
	body    string
	headers map[string]string
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	u.calls++
	for name, value := range u.headers {
		w.Header().Set(name, value)
	}
	w.Header().Set("Content-Type", "application/json")
	status := u.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	body := u.body
	if body == "" {
		body = upstreamBody
	}
	_, _ = w.Write([]byte(body))
}

// cachingConfig is an api config with one table opted in.
func cachingConfig(allowPublic bool) apiconfig.ApiConfig {
	cfg := apiconfig.DefaultApiConfig()
	cfg.Rest.CacheMaxTTL = 60
	cfg.Rest.CacheTables[cachedTable] = apiconfig.RestTableCacheConfig{Enabled: true, AllowPublic: allowPublic}
	return cfg
}

// staticStore serves one api config and never fails.
type staticStore struct {
	cfg apiconfig.ApiConfig
	err error
}

func (s staticStore) Get(context.Context) (apiconfig.ApiConfig, error) { return s.cfg, s.err }
func (s staticStore) Set(context.Context, apiconfig.ApiConfig) error   { return nil }

// deps builds a cache over this Valkey and api config.
func deps(cache valkey.Client, store apiconfig.Store) Deps {
	return Deps{
		Store:      store,
		Cache:      cache,
		Config:     &config.Config{Mode: "standalone", JWTSecret: "secret"},
		SchemaFor:  func(*http.Request) string { return "public" },
		MaxRowsFor: func(*http.Request) string { return "" },
		// Nothing is caller-dependent unless a test says so.
		IdentityScoped: func(context.Context) (map[string]bool, bool) {
			return map[string]bool{cachedTable: false}, true
		},
	}
}

// request builds a GET with the cache directive a caller would send.
func request(path, directive string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if directive != "" {
		req.Header.Set("X-Supatype-Cache", directive)
	}
	return req
}

// serve runs one request through the cache.
func serve(d Deps, next http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	Middleware(d, next).ServeHTTP(rec, req)
	return rec
}

// ─── The basic cycle ──────────────────────────────────────────────────────────

// The second identical request is answered from the cache, and says so.
func TestASecondRequestIsAHit(t *testing.T) {
	cache, next := valkeytest.New(), &upstream{}
	d := deps(cache, staticStore{cfg: cachingConfig(false)})

	first := serve(d, next, request("/posts", "max-age=30"))
	if got := first.Header().Get(statusHeader); got != "MISS" {
		t.Fatalf("first request: status = %q, want MISS (%s)", got, first.Body.String())
	}
	if first.Body.String() != upstreamBody {
		t.Fatalf("first body = %q", first.Body.String())
	}

	second := serve(d, next, request("/posts", "max-age=30"))
	if got := second.Header().Get(statusHeader); got != "HIT" {
		t.Fatalf("second request: status = %q, want HIT", got)
	}
	if second.Body.String() != upstreamBody {
		t.Errorf("second body = %q", second.Body.String())
	}
	if next.calls != 1 {
		t.Errorf("the upstream was reached %d times, want 1", next.calls)
	}
	if got := second.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q, want the stored one", got)
	}
	if got := second.Header().Get("Age"); got == "" {
		t.Error("a hit should report its age")
	}
	if got := second.Header().Get("Vary"); !strings.Contains(got, "Authorization") {
		t.Errorf("Vary = %q, want the auth headers named so a shared cache does not mix callers", got)
	}
}

// A stored entry older than the TTL the caller asked for is not served to them,
// even though it is still in Valkey for a caller who would accept it.
func TestAnEntryOlderThanTheAskedForTTLIsNotServed(t *testing.T) {
	cache, next := valkeytest.New(), &upstream{}
	d := deps(cache, staticStore{cfg: cachingConfig(false)})

	serve(d, next, request("/posts", "max-age=30"))

	// Rewrite the stored entry as though it were made a minute ago.
	keys := cache.Keys()
	if len(keys) != 1 {
		t.Fatalf("keys = %v, want one", keys)
	}
	stale, err := json.Marshal(Entry{StatusCode: http.StatusOK, Body: []byte(upstreamBody), CachedAt: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	cache.Put(keys[0], stale, 60)

	rec := serve(d, next, request("/posts", "max-age=30"))
	if got := rec.Header().Get(statusHeader); got != "MISS" {
		t.Errorf("status = %q, want MISS for an entry past its age", got)
	}
	if next.calls != 2 {
		t.Errorf("the upstream was reached %d times, want the stale entry to have been refetched", next.calls)
	}
}

// ─── What is not cached ───────────────────────────────────────────────────────

// The cache is opt-in from both ends: the caller has to ask and the table has to
// be configured for it.
func TestNothingIsCachedUnlessBothSidesAskedForIt(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg       apiconfig.ApiConfig
		path      string
		directive string
	}{
		"the caller did not ask":       {cachingConfig(false), "/posts", ""},
		"the caller asked for nothing": {cachingConfig(false), "/posts", "max-age=0"},
		"the table is not configured":  {cachingConfig(false), "/comments", "max-age=30"},
		"an RPC call":                  {cachingConfig(false), "/rpc/do_thing", "max-age=30"},
		"the server allows no TTL":     {apiconfig.DefaultApiConfig(), "/posts", "max-age=30"},
	} {
		cache, next := valkeytest.New(), &upstream{}
		rec := serve(deps(cache, staticStore{cfg: tc.cfg}), next, request(tc.path, tc.directive))

		if got := rec.Header().Get(statusHeader); got != "" {
			t.Errorf("%s: status = %q, want no cache header at all", name, got)
		}
		if len(cache.Keys()) != 0 {
			t.Errorf("%s: something was cached: %v", name, cache.Keys())
		}
		if rec.Body.String() != upstreamBody {
			t.Errorf("%s: the response was not passed through: %q", name, rec.Body.String())
		}
	}
}

// A write is never cached, and never even considered.
func TestOnlyReadsAreCached(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		cache, next := valkeytest.New(), &upstream{}
		req := httptest.NewRequest(method, "/posts", strings.NewReader("{}"))
		req.Header.Set("X-Supatype-Cache", "max-age=30")

		rec := serve(deps(cache, staticStore{cfg: cachingConfig(false)}), next, req)
		if got := rec.Header().Get(statusHeader); got != "" {
			t.Errorf("%s: status = %q", method, got)
		}
		if cache.Gets != 0 || cache.Sets != 0 {
			t.Errorf("%s: the cache was consulted for a write", method)
		}
	}
}

// A HEAD is cacheable but must not carry a body, on either the miss or the hit.
func TestHEADIsCachedWithoutABody(t *testing.T) {
	cache, next := valkeytest.New(), &upstream{}
	d := deps(cache, staticStore{cfg: cachingConfig(false)})

	head := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodHead, "/posts", nil)
		req.Header.Set("X-Supatype-Cache", "max-age=30")
		return serve(d, next, req)
	}

	if body := head().Body.String(); body != "" {
		t.Errorf("miss body = %q, want none for HEAD", body)
	}
	second := head()
	if second.Header().Get(statusHeader) != "HIT" {
		t.Errorf("status = %q, want HIT", second.Header().Get(statusHeader))
	}
	if body := second.Body.String(); body != "" {
		t.Errorf("hit body = %q, want none for HEAD", body)
	}
}

// A response the upstream would not want shared, or one too big to be worth
// keeping, is relayed but not stored.
func TestAResponseThatMustNotBeKept(t *testing.T) {
	for name, next := range map[string]*upstream{
		"an error status":        {status: http.StatusInternalServerError},
		"a redirect":             {status: http.StatusFound},
		"a not-found":            {status: http.StatusNotFound},
		"one that sets a cookie": {headers: map[string]string{"Set-Cookie": "session=abc"}},
		"a body over the cap":    {body: strings.Repeat("x", maxCacheBodyBytes+1)},
	} {
		cache := valkeytest.New()
		rec := serve(deps(cache, staticStore{cfg: cachingConfig(false)}), next, request("/posts", "max-age=30"))

		if got := rec.Header().Get(statusHeader); got != "MISS" {
			t.Errorf("%s: status = %q, want MISS", name, got)
		}
		if len(cache.Keys()) != 0 {
			t.Errorf("%s: it was stored anyway", name)
		}
		if got := rec.Header().Get("Vary"); got != "" {
			t.Errorf("%s: Vary = %q, want none on a response that was not cached", name, got)
		}
	}
}

// 206 is cacheable, because a Range request's answer is keyed by its Range
// header.
func TestAPartialResponseIsCached(t *testing.T) {
	cache := valkeytest.New()
	next := &upstream{status: http.StatusPartialContent}
	serve(deps(cache, staticStore{cfg: cachingConfig(false)}), next, request("/posts", "max-age=30"))

	if len(cache.Keys()) != 1 {
		t.Errorf("keys = %v, want the partial response cached", cache.Keys())
	}
}

// ─── Bypass ───────────────────────────────────────────────────────────────────

// BYPASS means "you asked and it did not happen", which is different from MISS.
// A caller watching the header can tell a cold cache from a broken one.
func TestBypassIsReportedWhenTheCallerAskedAndCouldNotBeServed(t *testing.T) {
	failing := valkeytest.New()
	failing.GetErr = valkeytest.ErrFailed

	for name, tc := range map[string]struct {
		deps Deps
		want string
	}{
		"the cache will not answer": {deps(failing, staticStore{cfg: cachingConfig(false)}), "BYPASS"},
		"there is no cache at all":  {deps(nil, staticStore{cfg: cachingConfig(false)}), "BYPASS"},
	} {
		rec := serve(tc.deps, &upstream{}, request("/posts", "max-age=30"))
		if got := rec.Header().Get(statusHeader); got != tc.want {
			t.Errorf("%s: status = %q, want %q", name, got, tc.want)
		}
		if rec.Body.String() != upstreamBody {
			t.Errorf("%s: the request was not served: %q", name, rec.Body.String())
		}
	}
}

// A cache that reads but will not write is a bypass, not a miss: nothing was
// stored, so the next request will not be a hit either.
func TestAFailedStoreIsABypass(t *testing.T) {
	cache := valkeytest.New()
	cache.SetErr = valkeytest.ErrFailed

	rec := serve(deps(cache, staticStore{cfg: cachingConfig(false)}), &upstream{}, request("/posts", "max-age=30"))
	if got := rec.Header().Get(statusHeader); got != "BYPASS" {
		t.Errorf("status = %q, want BYPASS", got)
	}
	if rec.Body.String() != upstreamBody {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// An entry that will not decode is a broken cache, not an empty one.
func TestACorruptEntryIsABypass(t *testing.T) {
	cache, next := valkeytest.New(), &upstream{}
	d := deps(cache, staticStore{cfg: cachingConfig(false)})

	serve(d, next, request("/posts", "max-age=30"))
	cache.Put(cache.Keys()[0], []byte("{not json"), 60)

	rec := serve(d, next, request("/posts", "max-age=30"))
	if got := rec.Header().Get(statusHeader); got != "BYPASS" {
		t.Errorf("status = %q, want BYPASS", got)
	}
	if next.calls != 2 {
		t.Errorf("the upstream was reached %d times, want the corrupt entry to have been refetched", next.calls)
	}
}

// A config that cannot be read means the cache cannot know what is allowed, so
// it does nothing at all — and says nothing, because the caller's request was
// not refused, it was simply not cached.
func TestAnUnreadableConfigCachesNothingQuietly(t *testing.T) {
	cache := valkeytest.New()
	d := deps(cache, staticStore{err: valkeytest.ErrFailed})

	rec := serve(d, &upstream{}, request("/posts", "max-age=30"))
	if got := rec.Header().Get(statusHeader); got != "" {
		t.Errorf("status = %q, want no header", got)
	}
	if len(cache.Keys()) != 0 {
		t.Error("something was cached without knowing whether it was allowed")
	}
}

// In managed mode a tenant that has not been granted the cache does not get it,
// and is told so when it asked.
func TestManagedModeHonoursTheTenantGrant(t *testing.T) {
	enabled, disabled := true, false

	for name, tc := range map[string]struct {
		tenantCache *bool
		want        string
	}{
		"the tenant has the cache":       {&enabled, "MISS"},
		"the tenant does not":            {&disabled, "BYPASS"},
		"the tenant config says nothing": {nil, "BYPASS"},
	} {
		cache := valkeytest.New().WithTenant("proj-1", &valkey.TenantConfig{RestCacheEnabled: tc.tenantCache})
		d := deps(cache, staticStore{cfg: cachingConfig(false)})
		d.Config = &config.Config{Mode: "managed", ManagedProjectRef: "proj-1", JWTSecret: "secret"}

		rec := serve(d, &upstream{}, request("/posts", "max-age=30"))
		if got := rec.Header().Get(statusHeader); got != tc.want {
			t.Errorf("%s: status = %q, want %q", name, got, tc.want)
		}
	}
}

// ─── Scope ────────────────────────────────────────────────────────────────────

// Two callers share an entry only when the table said so and the schema agrees
// that its read rule does not depend on who is asking.
func TestPublicScopeIsOnlyGrantedWhenItIsSafe(t *testing.T) {
	callerA := func() *http.Request {
		req := request("/posts", "max-age=30, public")
		req.Header.Set("Authorization", "Bearer token-a")
		return req
	}
	callerB := func() *http.Request {
		req := request("/posts", "max-age=30, public")
		req.Header.Set("Authorization", "Bearer token-b")
		return req
	}

	for name, tc := range map[string]struct {
		allowPublic bool
		scoped      IdentityScoped
		wantShared  bool
	}{
		"the table allows it and the rule does not vary": {
			true,
			func(context.Context) (map[string]bool, bool) { return map[string]bool{cachedTable: false}, true },
			true,
		},
		"the table allows it but the rule varies by caller": {
			true,
			func(context.Context) (map[string]bool, bool) { return map[string]bool{cachedTable: true}, true },
			false,
		},
		"the table does not allow it": {
			false,
			func(context.Context) (map[string]bool, bool) { return map[string]bool{cachedTable: false}, true },
			false,
		},
		"the classification cannot be read": {
			true,
			func(context.Context) (map[string]bool, bool) { return nil, false },
			false,
		},
		"the schema does not describe the table": {
			true,
			func(context.Context) (map[string]bool, bool) { return map[string]bool{}, true },
			false,
		},
		"there is no classification to consult": {true, nil, false},
	} {
		cache, next := valkeytest.New(), &upstream{}
		d := deps(cache, staticStore{cfg: cachingConfig(tc.allowPublic)})
		d.IdentityScoped = tc.scoped

		serve(d, next, callerA())
		second := serve(d, next, callerB())

		shared := second.Header().Get(statusHeader) == "HIT"
		if shared != tc.wantShared {
			t.Errorf("%s: the second caller got %q, shared = %v, want %v",
				name, second.Header().Get(statusHeader), shared, tc.wantShared)
		}
		if !tc.wantShared && next.calls != 2 {
			t.Errorf("%s: the upstream was reached %d times, want one per caller", name, next.calls)
		}
	}
}

// The stored entry records which scope it was cached under, because the admin
// API lists it and an operator needs to know whether an entry is shared.
func TestTheStoredEntryRecordsItsScope(t *testing.T) {
	for name, tc := range map[string]struct {
		directive string
		want      string
	}{
		"a per-caller entry": {"max-age=30", "user"},
		"a shared entry":     {"max-age=30, public", "public"},
	} {
		cache := valkeytest.New()
		d := deps(cache, staticStore{cfg: cachingConfig(true)})
		serve(d, &upstream{}, request("/posts", tc.directive))

		keys := cache.Keys()
		if len(keys) != 1 {
			t.Fatalf("%s: keys = %v", name, keys)
		}
		raw, err := cache.GetBytes(context.Background(), keys[0])
		if err != nil {
			t.Fatal(err)
		}
		entry, err := DecodeEntry(raw)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Scope != tc.want {
			t.Errorf("%s: scope = %q, want %q", name, entry.Scope, tc.want)
		}
		if entry.Table != cachedTable || entry.Path != "/posts" || entry.Method != http.MethodGet {
			t.Errorf("%s: entry = %+v, want it to record what it is", name, entry)
		}
	}
}

// Two callers never share a per-caller entry, which is the default and the
// thing that must not regress.
func TestTwoCallersDoNotShareAPerCallerEntry(t *testing.T) {
	cache, next := valkeytest.New(), &upstream{}
	d := deps(cache, staticStore{cfg: cachingConfig(false)})

	for _, token := range []string{"token-a", "token-b"} {
		req := request("/posts", "max-age=30")
		req.Header.Set("Authorization", "Bearer "+token)
		serve(d, next, req)
	}

	if next.calls != 2 {
		t.Errorf("the upstream was reached %d times, want one per caller", next.calls)
	}
	if len(cache.Keys()) != 2 {
		t.Errorf("keys = %v, want one per caller", cache.Keys())
	}
}

// Anything that changes what the upstream would return has to change the key,
// or one request's answer is served for another.
func TestTheKeyVariesWithEverythingThatChangesTheAnswer(t *testing.T) {
	base := func() *http.Request { return request("/posts", "max-age=30") }

	variants := map[string]func(*http.Request){
		"a different query":    func(r *http.Request) { r.URL.RawQuery = "select=id" },
		"a different path":     func(r *http.Request) { r.URL.Path = "/posts/nested" },
		"a different Accept":   func(r *http.Request) { r.Header.Set("Accept", "text/csv") },
		"a different Range":    func(r *http.Request) { r.Header.Set("Range", "0-9") },
		"a different Language": func(r *http.Request) { r.Header.Set("Accept-Language", "fr") },
		"a different caller":   func(r *http.Request) { r.Header.Set("Authorization", "Bearer other") },
	}

	for name, vary := range variants {
		cache, next := valkeytest.New(), &upstream{}
		d := deps(cache, staticStore{cfg: cachingConfig(false)})

		serve(d, next, base())
		varied := base()
		vary(varied)
		second := serve(d, next, varied)

		if second.Header().Get(statusHeader) == "HIT" {
			t.Errorf("%s: served a cached answer for a different request", name)
		}
	}
}

// The schema and the row cap are resolved per request and are part of the key,
// because the same URL against a different schema is a different query.
func TestTheKeyVariesWithTheResolvedSchemaAndRowCap(t *testing.T) {
	for name, tc := range map[string]struct {
		schemas []string
		maxRows []string
	}{
		"a different schema":  {[]string{"public", "other"}, []string{"", ""}},
		"a different row cap": {[]string{"public", "public"}, []string{"", "10"}},
	} {
		cache, next := valkeytest.New(), &upstream{}
		var call int
		d := deps(cache, staticStore{cfg: cachingConfig(false)})
		d.SchemaFor = func(*http.Request) string { return tc.schemas[call] }
		d.MaxRowsFor = func(*http.Request) string { return tc.maxRows[call] }

		serve(d, next, request("/posts", "max-age=30"))
		call = 1
		second := serve(d, next, request("/posts", "max-age=30"))

		if second.Header().Get(statusHeader) == "HIT" {
			t.Errorf("%s: served a cached answer across the change", name)
		}
	}
}

// ─── Relay ────────────────────────────────────────────────────────────────────

// The upstream's own headers reach the caller on a miss. Dropping them would
// lose the masked-fields header and Content-Range, among others.
func TestTheUpstreamsHeadersAreRelayed(t *testing.T) {
	next := &upstream{headers: map[string]string{
		"Content-Range":            "0-0/1",
		"X-Supatype-Masked-Fields": "ssn=identity",
	}}
	rec := serve(deps(valkeytest.New(), staticStore{cfg: cachingConfig(false)}), next, request("/posts", "max-age=30"))

	if got := rec.Header().Get("Content-Range"); got != "0-0/1" {
		t.Errorf("Content-Range = %q", got)
	}
	if got := rec.Header().Get("X-Supatype-Masked-Fields"); got != "ssn=identity" {
		t.Errorf("masked fields header = %q", got)
	}
}

// A request that is not cacheable at all still reaches the upstream untouched.
func TestANonCacheableRequestIsUntouched(t *testing.T) {
	next := &upstream{status: http.StatusTeapot, body: "brewing"}
	rec := serve(deps(valkeytest.New(), staticStore{cfg: apiconfig.DefaultApiConfig()}), next, request("/posts", ""))

	if rec.Code != http.StatusTeapot || rec.Body.String() != "brewing" {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
}
