package controlplane

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC) }

// The timestamp is inside the signed payload, so a captured header cannot be
// replayed indefinitely.
func TestSignCoversTimestampMethodAndPath(t *testing.T) {
	ts, sig := Sign("secret", "POST", "/internal/activity/proj", fixedNow())
	if ts != strings.TrimSpace(ts) || ts == "" {
		t.Errorf("timestamp = %q", ts)
	}
	if !strings.HasPrefix(sig, "v1=") {
		t.Errorf("signature should be versioned: %q", sig)
	}

	// Each signed component must actually change the signature.
	if _, other := Sign("secret", "POST", "/internal/activity/proj", fixedNow().Add(time.Second)); other == sig {
		t.Error("the timestamp does not affect the signature, so it can be replayed")
	}
	if _, s := Sign("secret", "GET", "/internal/activity/proj", fixedNow()); s == sig {
		t.Error("the method does not affect the signature")
	}
	if _, s := Sign("secret", "POST", "/internal/activity/other", fixedNow()); s == sig {
		t.Error("the path does not affect the signature")
	}
	if _, s := Sign("other", "POST", "/internal/activity/proj", fixedNow()); s == sig {
		t.Error("the secret does not affect the signature")
	}
}

// The method is upper-cased before signing, so a caller passing lower case still
// produces the signature the control plane will verify.
func TestSignNormalisesTheMethod(t *testing.T) {
	_, upper := Sign("s", "POST", "/p", fixedNow())
	_, lower := Sign("s", "post", "/p", fixedNow())
	if upper != lower {
		t.Error("the method case leaked into the signature")
	}
}

func newClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{
		BaseURL: srv.URL,
		Secret:  "secret",
		HTTP:    &http.Client{Timeout: 2 * time.Second},
		Now:     fixedNow,
	}
}

func TestTouchActivitySignsAndTargetsTheTenant(t *testing.T) {
	var gotPath, gotTS, gotSig, gotMethod string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotTS = r.Header.Get("X-Supatype-Internal-Ts")
		gotSig = r.Header.Get("X-Supatype-Internal-Sig")
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.TouchActivity(context.Background(), "proj-1"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/internal/activity/proj-1" {
		t.Errorf("got %s %s", gotMethod, gotPath)
	}
	wantTS, wantSig := Sign("secret", "POST", "/internal/activity/proj-1", fixedNow())
	if gotTS != wantTS || gotSig != wantSig {
		t.Errorf("signature headers = %q %q", gotTS, gotSig)
	}
}

func TestTouchActivityReportsFailure(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if err := c.TouchActivity(context.Background(), "proj"); err == nil {
		t.Error("a 500 must be reported")
	}

	unreachable := &Client{BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{Timeout: time.Second}}
	if err := unreachable.TouchActivity(context.Background(), "proj"); err == nil {
		t.Error("an unreachable control plane must be reported")
	}
}

// Without a secret the call still goes, unsigned. Whether to accept it is the
// control plane's decision, not this client's.
func TestUnsignedWhenNoSecret(t *testing.T) {
	var hadSig bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hadSig = r.Header.Get("X-Supatype-Internal-Sig") != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: &http.Client{Timeout: time.Second}}
	if err := c.TouchActivity(context.Background(), "proj"); err != nil {
		t.Fatal(err)
	}
	if hadSig {
		t.Error("no secret configured, so no signature should be sent")
	}
}

func TestOrgFor(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/projects/proj/org" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"org_id":"org-9"}`))
	})
	got, err := c.OrgFor(context.Background(), "proj")
	if err != nil {
		t.Fatal(err)
	}
	if got != "org-9" {
		t.Errorf("org = %q", got)
	}
}

func TestOrgForRejectsUnusableAnswers(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"not found":     func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"bad json":      func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{`)) },
		"empty org":     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"org_id":""}`)) },
		"missing field": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) },
	} {
		c := newClient(t, handler)
		if _, err := c.OrgFor(context.Background(), "proj"); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestRequestRejectsAnUnusableBaseURL(t *testing.T) {
	c := &Client{BaseURL: "://nonsense", HTTP: &http.Client{}}
	if err := c.TouchActivity(context.Background(), "p"); err == nil {
		t.Error("TouchActivity: want an error")
	}
	if _, err := c.OrgFor(context.Background(), "p"); err == nil {
		t.Error("OrgFor: want an error")
	}
}

// countingLookup records how often the control plane was actually asked.
type countingLookup struct {
	mu    sync.Mutex
	calls int
	org   string
	err   error
}

func (l *countingLookup) OrgFor(context.Context, string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return l.org, l.err
}

func (l *countingLookup) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func TestOrgCacheServesRepeatsFromMemory(t *testing.T) {
	lookup := &countingLookup{org: "org-1"}
	cache := &OrgCache{Lookup: lookup, Now: fixedNow}

	for i := 0; i < 5; i++ {
		got, ok := cache.Resolve(context.Background(), "proj")
		if !ok || got != "org-1" {
			t.Fatalf("resolve %d: got %q ok=%v", i, got, ok)
		}
	}
	if lookup.count() != 1 {
		t.Errorf("asked the control plane %d times, want 1", lookup.count())
	}
}

// The whole reason this cache was rewritten. Previously only answers were
// remembered, so a project that would not resolve was looked up again on every
// authentication, each time waiting on a network call from the request path.
func TestOrgCacheRemembersFailures(t *testing.T) {
	lookup := &countingLookup{err: errors.New("unknown project")}
	cache := &OrgCache{Lookup: lookup, Now: fixedNow}

	for i := 0; i < 10; i++ {
		if _, ok := cache.Resolve(context.Background(), "ghost"); ok {
			t.Fatal("an unresolvable project must not resolve")
		}
	}
	if lookup.count() != 1 {
		t.Errorf("asked about an unknown project %d times, want 1", lookup.count())
	}
}

// A failure may be a deploy racing a provision, so it is retried sooner than a
// success is re-checked.
func TestOrgCacheRetriesFailuresSoonerThanSuccesses(t *testing.T) {
	now := fixedNow()
	lookup := &countingLookup{err: errors.New("not yet")}
	cache := &OrgCache{
		Lookup:      lookup,
		TTL:         5 * time.Minute,
		NegativeTTL: 30 * time.Second,
		Now:         func() time.Time { return now },
	}

	cache.Resolve(context.Background(), "proj")
	now = now.Add(31 * time.Second)

	// The project has appeared by now.
	lookup.err = nil
	lookup.org = "org-2"
	got, ok := cache.Resolve(context.Background(), "proj")
	if !ok || got != "org-2" {
		t.Fatalf("got %q ok=%v; a failure should be retried after NegativeTTL", got, ok)
	}
	if lookup.count() != 2 {
		t.Errorf("calls = %d, want 2", lookup.count())
	}

	// The success now holds for the longer lifetime.
	now = now.Add(31 * time.Second)
	cache.Resolve(context.Background(), "proj")
	if lookup.count() != 2 {
		t.Errorf("a success re-checked after only NegativeTTL: calls = %d", lookup.count())
	}
}

func TestOrgCacheExpiresSuccesses(t *testing.T) {
	now := fixedNow()
	lookup := &countingLookup{org: "org-1"}
	cache := &OrgCache{Lookup: lookup, TTL: time.Minute, Now: func() time.Time { return now }}

	cache.Resolve(context.Background(), "proj")
	now = now.Add(61 * time.Second)
	cache.Resolve(context.Background(), "proj")

	if lookup.count() != 2 {
		t.Errorf("calls = %d, want the entry to have expired", lookup.count())
	}
}

func TestOrgCacheDefaultsAreSane(t *testing.T) {
	cache := &OrgCache{Lookup: &countingLookup{org: "o"}}
	if cache.ttl() != DefaultTTL {
		t.Errorf("ttl = %v", cache.ttl())
	}
	if cache.negativeTTL() != DefaultNegativeTTL {
		t.Errorf("negativeTTL = %v", cache.negativeTTL())
	}
	if cache.negativeTTL() >= cache.ttl() {
		t.Error("a failure should be retried sooner than a success is re-checked")
	}
	if cache.now().IsZero() {
		t.Error("the default clock should be the real one")
	}
}

func TestOrgCacheIsSafeUnderConcurrency(t *testing.T) {
	cache := &OrgCache{Lookup: &countingLookup{org: "org-1"}}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Resolve(context.Background(), "proj")
		}()
	}
	wg.Wait()
}

func TestClientUsesTheRealClockByDefault(t *testing.T) {
	var gotTS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTS = r.Header.Get("X-Supatype-Internal-Ts")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Secret: "s", HTTP: &http.Client{Timeout: time.Second}}
	if err := c.TouchActivity(context.Background(), "proj"); err != nil {
		t.Fatal(err)
	}
	if gotTS == "" || gotTS == "0" {
		t.Errorf("timestamp = %q, want the real clock", gotTS)
	}
}

// An org lookup against an unreachable control plane must report rather than
// hang or panic.
func TestOrgForOnAnUnreachableControlPlane(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{Timeout: time.Second}}
	if _, err := c.OrgFor(context.Background(), "proj"); err == nil {
		t.Error("want an error")
	}
}
