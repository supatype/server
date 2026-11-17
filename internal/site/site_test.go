package site

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/static"
)

const (
	testTenant = "proj"
	testToken  = "tok-proj"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// controlPlane stands in for the control plane's internal site routes.
type controlPlane struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	manifests map[string][]manifestFile
	// blobs is what a blob GET answers by hash, so a test can serve the wrong bytes.
	blobs        map[string]string
	blobGets     int
	manifestGets int
}

func newControlPlane(t *testing.T) *controlPlane {
	t.Helper()
	cp := &controlPlane{t: t, manifests: map[string][]manifestFile{}, blobs: map[string]string{}}
	cp.srv = httptest.NewServer(http.HandlerFunc(cp.serve))
	t.Cleanup(cp.srv.Close)
	return cp
}

// add registers a deployment whose manifest is honest about its files.
func (cp *controlPlane) add(id string, files map[string]string) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	var m []manifestFile
	for p, body := range files {
		m = append(m, manifestFile{Path: p, SHA256: sum(body), Size: int64(len(body))})
		cp.blobs[sum(body)] = body
	}
	cp.manifests[id] = m
}

// addManifest registers a deployment with a hand-written manifest.
func (cp *controlPlane) addManifest(id string, m []manifestFile) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.manifests[id] = m
}

func (cp *controlPlane) setBlob(hash, body string) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.blobs[hash] = body
}

func (cp *controlPlane) counts() (manifests, blobGets int) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.manifestGets, cp.blobGets
}

func (cp *controlPlane) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// internal / sites / :tenant / deployments / :id / manifest | blobs / :sha
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 6 || parts[0] != "internal" || parts[1] != "sites" || parts[2] != testTenant || parts[3] != "deployments" {
		http.NotFound(w, r)
		return
	}
	cp.mu.Lock()
	defer cp.mu.Unlock()
	m, ok := cp.manifests[parts[4]]
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case parts[5] == "manifest" && len(parts) == 6:
		cp.manifestGets++
		_ = json.NewEncoder(w).Encode(map[string]any{"files": m, "spa": false})
	case parts[5] == "blobs" && len(parts) == 7:
		cp.blobGets++
		body, ok := cp.blobs[parts[6]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	default:
		http.NotFound(w, r)
	}
}

func (cp *controlPlane) ref(id string) *proxy.SiteRef {
	return &proxy.SiteRef{DeploymentID: id, Version: "v-" + id, Token: testToken}
}

func newSyncer(t *testing.T, cp *controlPlane, mutate func(*Config)) *Syncer {
	t.Helper()
	cfg := Config{
		Dir:         t.TempDir(),
		SourceURL:   cp.srv.URL,
		TenantID:    testTenant,
		MaxBytes:    1 << 20,
		RetireAfter: 0,
		MinBackoff:  time.Millisecond,
		MaxBackoff:  5 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func mustSync(t *testing.T, s *Syncer, ref *proxy.SiteRef) {
	t.Helper()
	s.Want(ref)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatalf("sync %s: %v", ref.DeploymentID, err)
	}
}

func TestSyncServeThenSwap(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "one", "assets/app.js": "js-one"})
	cp.add("d2", map[string]string{"index.html": "two", "assets/app.js": "js-one"})
	s := newSyncer(t, cp, nil)

	mustSync(t, s, cp.ref("d1"))
	if rec := get(s.Handler(), "/"); rec.Code != 200 || rec.Body.String() != "one" {
		t.Fatalf("d1 /: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(s.Handler(), "/assets/app.js"); rec.Body.String() != "js-one" {
		t.Fatalf("d1 asset: %q", rec.Body.String())
	}

	_, before := cp.counts()
	mustSync(t, s, cp.ref("d2"))
	if rec := get(s.Handler(), "/"); rec.Body.String() != "two" {
		t.Fatalf("d2 /: %q", rec.Body.String())
	}
	if _, after := cp.counts(); after-before != 1 {
		t.Fatalf("d2 downloaded %d blobs, want 1 (the unchanged asset is already held)", after-before)
	}
	if st := s.Status(); st.Serving != "d2" || st.Want != "d2" || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestAWrongHashIsRefusedAndTheOldVersionKept(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "good"})
	s := newSyncer(t, cp, nil)
	mustSync(t, s, cp.ref("d1"))

	cp.addManifest("d2", []manifestFile{{Path: "index.html", SHA256: sum("claimed"), Size: 8}})
	cp.setBlob(sum("claimed"), "tampered")

	s.Want(cp.ref("d2"))
	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("a blob whose bytes do not match its hash was accepted")
	}
	if rec := get(s.Handler(), "/"); rec.Body.String() != "good" {
		t.Fatalf("after a refused sync, / = %q, want the old version", rec.Body.String())
	}
	if st := s.Status(); st.Serving != "d1" || st.Want != "d2" || st.Error == "" {
		t.Fatalf("status = %+v", st)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.Dir, "blobs", sum("claimed"))); err == nil {
		t.Fatal("the bad blob was kept")
	}
}

func TestUnsafePathsAreRefused(t *testing.T) {
	for _, p := range []string{"../x", "/etc/passwd", "a/../../x", "a//b", "./a", "a/./b", "", "a/", "a\\b", "a\x00b"} {
		cp := newControlPlane(t)
		cp.addManifest("d1", []manifestFile{{Path: p, SHA256: sum("x"), Size: 1}})
		cp.setBlob(sum("x"), "x")
		s := newSyncer(t, cp, nil)
		s.Want(cp.ref("d1"))
		if err := s.Reconcile(context.Background()); err == nil {
			t.Errorf("path %q was accepted", p)
		}
		if st := s.Status(); st.Serving != "" {
			t.Errorf("path %q: serving %q", p, st.Serving)
		}
	}
}

func TestDuplicatePathsAndBadHashesAreRefused(t *testing.T) {
	cp := newControlPlane(t)
	cp.addManifest("dup", []manifestFile{
		{Path: "a.html", SHA256: sum("x"), Size: 1},
		{Path: "a.html", SHA256: sum("x"), Size: 1},
	})
	cp.addManifest("badsha", []manifestFile{{Path: "a.html", SHA256: "../../etc/passwd", Size: 1}})
	s := newSyncer(t, cp, nil)
	for _, id := range []string{"dup", "badsha"} {
		s.Want(cp.ref(id))
		if err := s.Reconcile(context.Background()); err == nil {
			t.Errorf("deployment %s was accepted", id)
		}
	}
}

func TestACompleteVersionFolderIsReused(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "one"})
	cp.add("d2", map[string]string{"index.html": "two"})
	s := newSyncer(t, cp, nil)
	mustSync(t, s, cp.ref("d1"))
	mustSync(t, s, cp.ref("d2"))

	m0, b0 := cp.counts()
	// Rolling back: d1 is still on disk and complete, so nothing is fetched.
	mustSync(t, s, cp.ref("d1"))
	if m1, b1 := cp.counts(); m1 != m0 || b1 != b0 {
		t.Fatalf("rollback fetched (manifests %d to %d, blobs %d to %d)", m0, m1, b0, b1)
	}
	if rec := get(s.Handler(), "/"); rec.Body.String() != "one" {
		t.Fatalf("after rollback / = %q", rec.Body.String())
	}

	// A restarted container: a new syncer on the same folder serves without any fetch.
	s2, err := New(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustSync(t, s2, cp.ref("d2"))
	if m1, b1 := cp.counts(); m1 != m0 || b1 != b0 {
		t.Fatalf("restart fetched (manifests %d to %d, blobs %d to %d)", m0, m1, b0, b1)
	}
	if rec := get(s2.Handler(), "/"); rec.Body.String() != "two" {
		t.Fatalf("after restart / = %q", rec.Body.String())
	}
}

func TestAnIncompleteVersionFolderIsRebuilt(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "one"})
	s := newSyncer(t, cp, nil)
	dir := filepath.Join(s.cfg.Dir, "versions", "d1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustSync(t, s, cp.ref("d1"))
	if rec := get(s.Handler(), "/"); rec.Body.String() != "one" {
		t.Fatalf("/ = %q, want a rebuilt version", rec.Body.String())
	}
}

func TestNoSiteIs404AndUnsyncedIs503(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "one"})
	s := newSyncer(t, cp, nil)

	if rec := get(s.Handler(), "/"); rec.Code != http.StatusNotFound {
		t.Fatalf("no site: %d, want 404", rec.Code)
	}
	s.Want(cp.ref("d1"))
	rec := get(s.Handler(), "/")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("not yet synced: %d Retry-After %q, want 503 and 5", rec.Code, rec.Header().Get("Retry-After"))
	}
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec := get(s.Handler(), "/"); rec.Code != 200 {
		t.Fatalf("synced: %d", rec.Code)
	}
	s.Want(nil)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec := get(s.Handler(), "/"); rec.Code != http.StatusNotFound {
		t.Fatalf("site removed: %d, want 404", rec.Code)
	}
}

func listVersions(t *testing.T, s *Syncer) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.cfg.Dir, "versions"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestPrunesToTheServedVersionPlusOne(t *testing.T) {
	cp := newControlPlane(t)
	for _, id := range []string{"d1", "d2", "d3"} {
		cp.add(id, map[string]string{"index.html": "page-" + id, "only-" + id + ".txt": id})
	}
	s := newSyncer(t, cp, nil)
	for _, id := range []string{"d1", "d2", "d3"} {
		mustSync(t, s, cp.ref(id))
	}
	if got := listVersions(t, s); len(got) != 2 || got[0] != "d2" || got[1] != "d3" {
		t.Fatalf("versions on disk = %v, want [d2 d3]", got)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.Dir, "blobs", sum("d1"))); err == nil {
		t.Fatal("a blob no kept version uses was not deleted")
	}
	if _, err := os.Stat(filepath.Join(s.cfg.Dir, "blobs", sum("d2"))); err != nil {
		t.Fatalf("a blob a kept version uses was deleted: %v", err)
	}
}

func TestOverBudgetIsRefused(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": strings.Repeat("a", 600)})
	cp.add("small", map[string]string{"index.html": "ok"})
	s := newSyncer(t, cp, func(c *Config) { c.MaxBytes = 500 })

	mustSync(t, s, cp.ref("small"))
	s.Want(cp.ref("d1"))
	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("a deployment over the byte budget was accepted")
	}
	if rec := get(s.Handler(), "/"); rec.Body.String() != "ok" {
		t.Fatalf("/ = %q, want the version already served", rec.Body.String())
	}
	if _, b := cp.counts(); b != 1 {
		t.Fatalf("blob fetches = %d, an over budget deployment must be refused before downloading", b)
	}
}

func TestADeclaredSizeIsEnforced(t *testing.T) {
	cp := newControlPlane(t)
	body := "longer than declared"
	cp.addManifest("d1", []manifestFile{{Path: "index.html", SHA256: sum(body), Size: 3}})
	cp.setBlob(sum(body), body)
	s := newSyncer(t, cp, nil)
	s.Want(cp.ref("d1"))
	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("a blob larger than its declared size was accepted")
	}
}

// blockingWriter stalls its first write until released, to hold a request in flight.
type blockingWriter struct {
	*httptest.ResponseRecorder
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	b.once.Do(func() {
		close(b.started)
		<-b.release
	})
	return b.ResponseRecorder.Write(p)
}

func TestARequestInFlightFinishesOnTheOldVersion(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "old-version"})
	cp.add("d2", map[string]string{"index.html": "new-version"})
	s := newSyncer(t, cp, func(c *Config) { c.RetireAfter = time.Hour })
	mustSync(t, s, cp.ref("d1"))

	w := &blockingWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	<-w.started
	mustSync(t, s, cp.ref("d2"))
	close(w.release)
	<-done

	if w.Body.String() != "old-version" {
		t.Fatalf("in-flight request finished with %q, want old-version", w.Body.String())
	}
	if rec := get(s.Handler(), "/"); rec.Body.String() != "new-version" {
		t.Fatalf("next request = %q, want new-version", rec.Body.String())
	}
}

func TestRunFollowsTheLatestTargetAndRetries(t *testing.T) {
	cp := newControlPlane(t)
	s := newSyncer(t, cp, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	// d1 does not exist yet: the worker backs off and keeps trying.
	s.Want(cp.ref("d1"))
	waitFor(t, func() bool { return s.Status().Error != "" })
	if st := s.Status(); st.Serving != "" {
		t.Fatalf("status while the control plane has nothing = %+v", st)
	}
	cp.add("d1", map[string]string{"index.html": "late"})
	waitFor(t, func() bool { return s.Status().Serving == "d1" })
	if st := s.Status(); st.Error != "" {
		t.Fatalf("error not cleared after success: %+v", st)
	}
	if rec := get(s.Handler(), "/"); rec.Body.String() != "late" {
		t.Fatalf("/ = %q", rec.Body.String())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestSPAFlagAndCacheOptsReachTheStaticHandler(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "shell"})
	s := newSyncer(t, cp, func(c *Config) { c.Cache = func() static.CacheOpts { return static.CacheOpts{HTML: "no-cache, x"} } })
	ref := cp.ref("d1")
	ref.SPA = true
	mustSync(t, s, ref)
	rec := get(s.Handler(), "/some/route")
	if rec.Code != 200 || rec.Body.String() != "shell" {
		t.Fatalf("spa fallback: %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache, x" {
		t.Fatalf("Cache-Control = %q", got)
	}
}
