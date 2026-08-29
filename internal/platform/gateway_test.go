package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/platform/controlplane"
	"github.com/supatype/server/internal/platform/mau"
)

// recordingStore captures MAU writes.
type recordingStore struct {
	mu      sync.Mutex
	members []string
}

func (s *recordingStore) AddToExpiringSet(_ context.Context, _, member string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members = append(s.members, member)
	return nil
}

func (s *recordingStore) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.members...)
}

// staticOrg resolves every project to one organisation.
type staticOrg struct {
	mu    sync.Mutex
	calls int
}

func (o *staticOrg) OrgFor(context.Context, string) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	return "org-1", nil
}

func (o *staticOrg) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

// testGateway builds a Gateway wired to fakes, bypassing New so no network is
// involved.
func testGateway(store mau.Store, org controlplane.OrgLookup) *Gateway {
	return &Gateway{
		tenantID: "proj-1",
		control:  &controlplane.Client{BaseURL: "http://unused", HTTP: &http.Client{}},
		orgs:     &controlplane.OrgCache{Lookup: org},
		recorder: mau.Recorder{Store: store},
		queue:    newTestQueue(),
	}
}

// A disabled gateway must be completely transparent.
func TestNilGatewayIsTransparent(t *testing.T) {
	var g *Gateway
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	g.Wrap(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rest/v1/things", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want the inner handler untouched", rec.Code)
	}
	g.Close()
	if g.Dropped() != 0 {
		t.Error("a nil gateway has dropped nothing")
	}
}

func TestNewReturnsNilWhenMeteringIsOff(t *testing.T) {
	if g := New(&config.Config{}, &recordingStore{}); g != nil {
		t.Error("metering is off by default, so New should return nil")
	}
}

func TestNewBuildsAGatewayWhenEnabled(t *testing.T) {
	cfg := &config.Config{ManagedProjectRef: "proj-1"}
	_ = cfg.CloudActivityEnabled.Decode("true")

	g := New(cfg, &recordingStore{})
	if g == nil {
		t.Fatal("want a gateway when metering is enabled")
	}
	defer g.Close()
	if g.tenantID != "proj-1" {
		t.Errorf("tenantID = %q", g.tenantID)
	}
	// Empty configuration must still yield a usable control plane address.
	if g.control.BaseURL == "" {
		t.Error("the control plane address should fall back to the default")
	}
}

// A successful sign-in is counted, and the caller does not wait for it.
func TestSuccessfulSignInIsCounted(t *testing.T) {
	store := &recordingStore{}
	org := &staticOrg{}
	g := testGateway(store, org)
	defer g.Close()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":{"id":"user-9"}}`))
	})

	rec := httptest.NewRecorder()
	g.Wrap(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/v1/token?grant_type=password", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "user-9") {
		t.Errorf("the response must reach the caller unchanged: %q", body)
	}

	g.Close() // waits for the queued tally
	if got := store.recorded(); len(got) != 1 || got[0] != "local:proj-1:user-9" {
		t.Errorf("recorded = %v", got)
	}
	if org.count() != 1 {
		t.Errorf("org lookups = %d", org.count())
	}
}

func TestIneligibleAndFailedCallsAreNotCounted(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		status int
	}{
		{"a refresh is not a person arriving", http.MethodPost, "/auth/v1/token?grant_type=refresh_token", 200},
		{"a failed sign-in is not a user", http.MethodPost, "/auth/v1/token?grant_type=password", 401},
		{"logout is not a sign-in", http.MethodPost, "/auth/v1/logout", 200},
		{"a data call is not auth", http.MethodGet, "/rest/v1/things", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &recordingStore{}
			g := testGateway(store, &staticOrg{})
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(`{"user":{"id":"user-9"}}`))
			})
			g.Wrap(inner).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(c.method, c.target, nil))
			g.Close()

			if got := store.recorded(); len(got) != 0 {
				t.Errorf("recorded %v, want nothing", got)
			}
		})
	}
}

// The previous implementation buffered the whole auth response with no ceiling,
// on every successful auth request, to read one small object out of it.
func TestLargeResponsesAreNotBufferedWithoutLimit(t *testing.T) {
	store := &recordingStore{}
	g := testGateway(store, &staticOrg{})
	defer g.Close()

	huge := strings.Repeat("x", captureLimit*2)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"padding":%q,"user":{"id":"user-9"}}`, huge)
	})

	rec := httptest.NewRecorder()
	g.Wrap(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/v1/signup", nil))

	// The caller still gets every byte.
	if rec.Body.Len() < len(huge) {
		t.Errorf("the response was truncated for the caller: %d bytes", rec.Body.Len())
	}
	// The tally is skipped rather than guessed at from a truncated body.
	g.Close()
	if got := store.recorded(); len(got) != 0 {
		t.Errorf("a truncated body must not produce a tally, got %v", got)
	}
}

func TestBoundedRecorderCapsWhatItKeeps(t *testing.T) {
	rec := httptest.NewRecorder()
	b := &boundedRecorder{ResponseWriter: rec, status: http.StatusOK, limit: 8}

	n, err := b.Write([]byte("0123456789"))
	if err != nil || n != 10 {
		t.Fatalf("Write = %d, %v; the caller must receive everything", n, err)
	}
	if b.body.Len() != 8 {
		t.Errorf("kept %d bytes, want the limit of 8", b.body.Len())
	}
	if !b.truncated {
		t.Error("truncation must be recorded")
	}
	// Writes after the limit are still passed through.
	if _, err := b.Write([]byte("more")); err != nil {
		t.Fatal(err)
	}
	if rec.Body.Len() != 14 {
		t.Errorf("the caller received %d bytes, want 14", rec.Body.Len())
	}
}

func TestBoundedRecorderRecordsTheStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	b := &boundedRecorder{ResponseWriter: rec, status: http.StatusOK, limit: 16}
	b.WriteHeader(http.StatusUnauthorized)
	if b.status != http.StatusUnauthorized || rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d / %d", b.status, rec.Code)
	}
	// Flush must reach the underlying writer, or a streaming response would be
	// held back by the wrapper.
	b.Flush()
	if !rec.Flushed {
		t.Error("Flush did not reach the ResponseWriter")
	}
}

func TestCountsAsActivity(t *testing.T) {
	req := func(path, ua string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if ua != "" {
			r.Header.Set("User-Agent", ua)
		}
		return r
	}
	for name, tc := range map[string]struct {
		r    *http.Request
		want bool
	}{
		"a person browsing":    {req("/rest/v1/things", "Mozilla/5.0"), true},
		"a crawler":            {req("/", "Googlebot/2.1"), false},
		"the platform's probe": {req("/health", ""), false},
		"the other probe path": {req("/healthz", ""), false},
	} {
		if got := countsAsActivity(tc.r); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}

	prefetch := req("/", "Mozilla/5.0")
	prefetch.Header.Set("Sec-Purpose", "prefetch")
	if countsAsActivity(prefetch) {
		t.Error("a speculative prefetch is not somebody using the project")
	}
}

// Non-production robots handling short-circuits before anything else.
func TestRobotsPolicyIsAppliedFirst(t *testing.T) {
	g := testGateway(&recordingStore{}, &staticOrg{})
	defer g.Close()
	g.policy.NonProd = true

	var reached bool
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })

	rec := httptest.NewRecorder()
	g.Wrap(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if reached {
		t.Error("robots.txt should be answered by the gateway")
	}
	if !strings.Contains(rec.Body.String(), "Disallow: /") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// Metering must never be a reason a customer's request fails or hangs.
func TestAFailingControlPlaneDoesNotAffectTheResponse(t *testing.T) {
	store := &recordingStore{}
	g := testGateway(store, failingOrg{})
	defer g.Close()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"id":"user-9"}}`))
	})

	rec := httptest.NewRecorder()
	g.Wrap(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/v1/signup", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; metering must fail open", rec.Code)
	}
	g.Close()
	if got := store.recorded(); len(got) != 0 {
		t.Errorf("no org means no tally, got %v", got)
	}
}

type failingOrg struct{}

func (failingOrg) OrgFor(context.Context, string) (string, error) {
	return "", fmt.Errorf("control plane unavailable")
}

// A response that is valid JSON but carries no user is not a sign-in.
func TestResponseWithoutAUserIsNotCounted(t *testing.T) {
	for name, body := range map[string]string{
		"no user field":    `{"access_token":"abc"}`,
		"user is null":     `{"user":null}`,
		"not json":         `<html>no</html>`,
		"user is a string": `{"user":"someone"}`,
	} {
		store := &recordingStore{}
		g := testGateway(store, &staticOrg{})
		inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		})
		g.Wrap(inner).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/v1/signup", nil))
		g.Close()

		if got := store.recorded(); len(got) != 0 {
			t.Errorf("%s: recorded %v", name, got)
		}
	}
}

// A gateway with no tenant id cannot attribute anything, so it must not try.
func TestNoTenantIDMeansNoTelemetry(t *testing.T) {
	store := &recordingStore{}
	org := &staticOrg{}
	g := testGateway(store, org)
	g.tenantID = ""
	defer g.Close()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"id":"u"}}`))
	})
	g.Wrap(inner).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/v1/signup", nil))
	g.Close()

	if got := store.recorded(); len(got) != 0 {
		t.Errorf("recorded %v", got)
	}
	if org.count() != 0 {
		t.Errorf("looked up an org %d times with no tenant", org.count())
	}
}

// A staging stack bills to the project's organisation, so its tenant id must be
// stripped before the lookup.
func TestStagingTenantResolvesToTheProject(t *testing.T) {
	store := &recordingStore{}
	g := testGateway(store, &staticOrg{})
	g.tenantID = "proj-1-staging"
	defer g.Close()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"id":"u"}}`))
	})
	g.Wrap(inner).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/v1/signup", nil))
	g.Close()

	got := store.recorded()
	if len(got) != 1 || got[0] != "local:proj-1:u" {
		t.Errorf("recorded %v, want the project ref without the env suffix", got)
	}
}

func TestActivityIsReportedToTheControlPlane(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g := testGateway(&recordingStore{}, &staticOrg{})
	g.control = &controlplane.Client{BaseURL: srv.URL, HTTP: &http.Client{Timeout: time.Second}}
	defer g.Close()

	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	g.Wrap(inner).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/rest/v1/things", nil))
	g.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/internal/activity/proj-1" {
		t.Errorf("control plane saw %v", paths)
	}
}

// A JSON body arriving in several writes must still be readable.
func TestChunkedResponseIsReassembled(t *testing.T) {
	store := &recordingStore{}
	g := testGateway(store, &staticOrg{})
	defer g.Close()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for _, chunk := range []string{`{"user":`, `{"id":`, `"user-9"}}`} {
			_, _ = w.Write([]byte(chunk))
		}
	})
	g.Wrap(inner).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/v1/signup", nil))
	g.Close()

	if got := store.recorded(); len(got) != 1 {
		t.Errorf("recorded %v, want one tally", got)
	}
}

func TestJSONShapeAssumption(t *testing.T) {
	// Guards the assumption the capture relies on: the user object is a
	// top-level field of the auth response.
	var payload map[string]any
	if err := json.Unmarshal([]byte(`{"user":{"id":"x"}}`), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["user"].(map[string]any); !ok {
		t.Fatal("the auth response shape this package reads has changed")
	}
}

// Dropped work is reported, so a saturated queue is visible rather than silent.
func TestDroppedIsReported(t *testing.T) {
	g := testGateway(&recordingStore{}, &staticOrg{})
	defer g.Close()
	if g.Dropped() != 0 {
		t.Errorf("a fresh gateway has dropped nothing, got %d", g.Dropped())
	}
}

// A store that refuses the write must not affect anything the customer sees.
func TestAFailingStoreIsSurvivable(t *testing.T) {
	g := testGateway(failingStore{}, &staticOrg{})
	defer g.Close()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"id":"u"}}`))
	})
	rec := httptest.NewRecorder()
	g.Wrap(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/v1/signup", nil))
	g.Close()

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; a failed tally must not reach the caller", rec.Code)
	}
}

type failingStore struct{}

func (failingStore) AddToExpiringSet(context.Context, string, string, time.Time) error {
	return fmt.Errorf("valkey unavailable")
}
