package site

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/supatype/server/internal/proxy"
)

func mustErrContain(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}
}

// servedBody is what the syncer serves at /, so a test can show the previous version survived.
func servedBody(s *Syncer) string { return get(s.Handler(), "/").Body.String() }

func ownRef() *proxy.SiteRef {
	return &proxy.SiteRef{DeploymentID: "d1", Version: "v-d1", Token: testToken}
}

func TestNewRefusesAnIncompleteConfigAndAnUnwritableDir(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no dir":    {SourceURL: "http://x", TenantID: "t"},
		"no source": {Dir: t.TempDir(), TenantID: "t"},
		"no tenant": {Dir: t.TempDir(), SourceURL: "http://x"},
	} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("%s: New accepted an incomplete config", name)
		}
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{Dir: filepath.Join(blocker, "sub"), SourceURL: "http://x", TenantID: "t"})
	mustErrContain(t, err, "site:")
}

func TestAnInvalidDeploymentIDIsRefusedBeforeAnyRequest(t *testing.T) {
	cp := newControlPlane(t)
	s := newSyncer(t, cp, nil)
	s.Want(cp.ref("../escape"))
	mustErrContain(t, s.Reconcile(context.Background()), "is not valid")
	if m, _ := cp.counts(); m != 0 {
		t.Fatalf("the control plane was asked %d times for an id that is not valid", m)
	}
}

func TestReconcilingTheServedVersionAgainFetchesNothing(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "one"})
	s := newSyncer(t, cp, nil)
	mustSync(t, s, cp.ref("d1"))
	m1, b1 := cp.counts()
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m2, b2 := cp.counts(); m2 != m1 || b2 != b1 {
		t.Fatalf("a second reconcile fetched again: manifests %d to %d, blobs %d to %d", m1, m2, b1, b2)
	}
	if got := servedBody(s); got != "one" {
		t.Fatalf("served %q", got)
	}
}

func TestSleepStopsWhenTheContextEndsDuringATimedWait(t *testing.T) {
	s := newSyncer(t, newControlPlane(t), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.sleep(ctx, time.Hour) {
		t.Fatal("sleep kept going after its context ended")
	}
}

func TestADeploymentOverBudgetInTotalIsRefused(t *testing.T) {
	cp := newControlPlane(t)
	s := newSyncer(t, cp, func(c *Config) { c.MaxBytes = 10 })
	cp.add("d1", map[string]string{"a.txt": "123456", "b.txt": "abcdef"})
	s.Want(cp.ref("d1"))
	mustErrContain(t, s.Reconcile(context.Background()), "byte budget")
	if st := s.Status(); st.Serving != "" {
		t.Fatalf("an over budget deployment is being served: %+v", st)
	}
}

func TestAnUnbuildableRequestAndAnUnreachableServerAreErrors(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "x"})
	bad := newSyncer(t, cp, func(c *Config) { c.SourceURL = "http://127.0.0.1:1\x7f" })
	bad.Want(cp.ref("d1"))
	if err := bad.Reconcile(context.Background()); err == nil {
		t.Fatal("a source URL that cannot form a request was accepted")
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	down := newSyncer(t, cp, func(c *Config) { c.SourceURL = url })
	down.Want(cp.ref("d1"))
	mustErrContain(t, down.Reconcile(context.Background()), "site: fetch")
}

func TestAManifestThatIsNotJSONIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not json"))
	}))
	t.Cleanup(srv.Close)
	s := newSyncer(t, newControlPlane(t), func(c *Config) { c.SourceURL = srv.URL })
	s.Want(ownRef())
	mustErrContain(t, s.Reconcile(context.Background()), "manifest")
}

func TestAMissingBlobKeepsThePreviousVersion(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "old"})
	s := newSyncer(t, cp, nil)
	mustSync(t, s, cp.ref("d1"))
	cp.addManifest("d2", []manifestFile{{Path: "index.html", SHA256: sum("never uploaded"), Size: 14}})
	s.Want(cp.ref("d2"))
	mustErrContain(t, s.Reconcile(context.Background()), "404")
	if got := servedBody(s); got != "old" {
		t.Fatalf("served %q, want the previous version", got)
	}
}

func TestADownloadThatCannotBeStagedIsAnError(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "x"})
	s := newSyncer(t, cp, nil)
	if err := os.RemoveAll(filepath.Join(s.cfg.Dir, "blobs")); err != nil {
		t.Fatal(err)
	}
	s.Want(cp.ref("d1"))
	mustErrContain(t, s.Reconcile(context.Background()), "site:")
}

// blobServer serves a one file deployment (index.html holding body) whose blob is answered by blob.
func blobServer(t *testing.T, body string, blob http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			_, _ = w.Write([]byte(`{"files":[{"path":"index.html","sha256":"` + sum(body) + `","size":` + strconv.Itoa(len(body)) + `}]}`))
			return
		}
		blob(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestABlobCutShortMidStreamIsAnError(t *testing.T) {
	srv := blobServer(t, "0123456789", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write([]byte("0123"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})
	s := newSyncer(t, newControlPlane(t), func(c *Config) { c.SourceURL = srv.URL })
	s.Want(ownRef())
	mustErrContain(t, s.Reconcile(context.Background()), "download")
	if st := s.Status(); st.Serving != "" {
		t.Fatalf("a truncated blob was served: %+v", st)
	}
}

func TestABlobThatCannotTakeItsNameIsAnError(t *testing.T) {
	const body = "0123456789"
	var dir string
	srv := blobServer(t, body, func(w http.ResponseWriter, _ *http.Request) {
		// A non-empty folder already holds the blob's name, so the rename into blobs/ must fail.
		_ = os.MkdirAll(filepath.Join(dir, "blobs", sum(body), "child"), 0o700)
		_, _ = w.Write([]byte(body))
	})
	s := newSyncer(t, newControlPlane(t), func(c *Config) { c.SourceURL = srv.URL })
	dir = s.cfg.Dir
	s.Want(ownRef())
	mustErrContain(t, s.Reconcile(context.Background()), "site:")
}

func TestLinkFailuresLeaveNothingServed(t *testing.T) {
	x := sum("x")
	cases := map[string]struct {
		files []manifestFile
		want  string
	}{
		"a file where an earlier file made a folder": {
			[]manifestFile{{Path: "a/b", SHA256: x, Size: 1}, {Path: "a", SHA256: x, Size: 1}}, "link"},
		"a folder under a file": {
			[]manifestFile{{Path: "a", SHA256: x, Size: 1}, {Path: "a/b", SHA256: x, Size: 1}}, "site:"},
		"a folder in the place of the blob list": {
			[]manifestFile{{Path: blobsMarker + "/x", SHA256: x, Size: 1}}, "site:"},
		"a folder in the place of the complete marker": {
			[]manifestFile{{Path: completeMarker + "/x", SHA256: x, Size: 1}}, "site:"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cp := newControlPlane(t)
			cp.setBlob(x, "x")
			cp.addManifest("d1", tc.files)
			s := newSyncer(t, cp, nil)
			s.Want(cp.ref("d1"))
			mustErrContain(t, s.Reconcile(context.Background()), tc.want)
			if st := s.Status(); st.Serving != "" {
				t.Fatalf("served after a failed link: %+v", st)
			}
			if names := listVersions(t, s); len(names) != 0 {
				t.Fatalf("a failed link left %v on disk", names)
			}
		})
	}
}

func TestLinkNeedsTheSiteDirAndItsVersionsFolder(t *testing.T) {
	breakers := map[string]func(dir string) error{
		"dir gone": os.RemoveAll,
		"versions a file": func(dir string) error {
			v := filepath.Join(dir, "versions")
			if err := os.RemoveAll(v); err != nil {
				return err
			}
			return os.WriteFile(v, []byte("x"), 0o600)
		},
	}
	for name, breakIt := range breakers {
		t.Run(name, func(t *testing.T) {
			cp := newControlPlane(t)
			cp.addManifest("d1", nil)
			s := newSyncer(t, cp, nil)
			if err := breakIt(s.cfg.Dir); err != nil {
				t.Fatal(err)
			}
			s.Want(cp.ref("d1"))
			mustErrContain(t, s.Reconcile(context.Background()), "site:")
		})
	}
}

func swapFS(t *testing.T, rm func(string) error, mv func(string, string) error) {
	t.Helper()
	oldRm, oldMv := removeAll, rename
	t.Cleanup(func() { removeAll, rename = oldRm, oldMv })
	if rm != nil {
		removeAll = rm
	}
	if mv != nil {
		rename = mv
	}
}

func TestAFolderThatCannotBeReplacedOrRenamedIsAnError(t *testing.T) {
	boom := errors.New("disk said no")
	cases := map[string]struct {
		rm func(string) error
		mv func(string, string) error
	}{
		"remove": {rm: func(string) error { return boom }},
		"rename": {mv: func(string, string) error { return boom }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cp := newControlPlane(t)
			cp.add("d1", map[string]string{"index.html": "x"})
			s := newSyncer(t, cp, nil)
			swapFS(t, tc.rm, tc.mv)
			s.Want(cp.ref("d1"))
			mustErrContain(t, s.Reconcile(context.Background()), "disk said no")
			if st := s.Status(); st.Serving != "" || st.Error == "" {
				t.Fatalf("status after a failed swap = %+v", st)
			}
		})
	}
}

func TestPruneSweepsCrashedBuildsAndSurvivesAMissingVersionsFolder(t *testing.T) {
	s := newSyncer(t, newControlPlane(t), nil)
	crashed := filepath.Join(s.cfg.Dir, "versions", "d9.tmp-123")
	if err := os.MkdirAll(crashed, 0o700); err != nil {
		t.Fatal(err)
	}
	s.prune()
	if _, err := os.Stat(crashed); !os.IsNotExist(err) {
		t.Fatalf("a crashed build survived prune: %v", err)
	}

	hook := logtest.NewGlobal()
	t.Cleanup(hook.Reset)
	if err := os.RemoveAll(filepath.Join(s.cfg.Dir, "versions")); err != nil {
		t.Fatal(err)
	}
	s.prune()
	if e := hook.LastEntry(); e == nil || e.Level != logrus.WarnLevel || !strings.Contains(e.Message, "list versions") {
		t.Fatalf("no warning for an unreadable versions folder: %+v", e)
	}
}

func TestRetireWaitsOnceAndNeverRemovesTheServedVersion(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "one"})
	s := newSyncer(t, cp, func(c *Config) { c.RetireAfter = 40 * time.Millisecond })
	mustSync(t, s, cp.ref("d1"))

	old := s.versionDir("old")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	// retire runs under s.mu, as prune calls it from Reconcile.
	s.mu.Lock()
	s.retire("old")
	s.retire("old") // already waiting: it must not schedule a second removal
	pending := len(s.retiring)
	s.mu.Unlock()
	if pending != 1 {
		t.Fatalf("%d versions waiting, want 1", pending)
	}
	waitFor(t, func() bool { _, err := os.Stat(old); return os.IsNotExist(err) })

	// A version that became the served one while it waited stays.
	s.mu.Lock()
	s.retire("d1")
	s.mu.Unlock()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.retiring) == 0 })
	if got := servedBody(s); got != "one" {
		t.Fatalf("the served version was removed by its retirement: %q", got)
	}
}

func TestARemovalThatFailsIsLoggedAndTheFolderKept(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(hook.Reset)
	swapFS(t, func(string) error { return errors.New("busy") }, nil)
	dir := t.TempDir()
	removeVersion(dir)
	if e := hook.LastEntry(); e == nil || !strings.Contains(e.Message, "could not remove") {
		t.Fatalf("no warning for a failed removal: %+v", e)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the folder is gone: %v", err)
	}
}

func TestCollectBlobsKeepsEverythingWhenTheBlobListCannotBeRead(t *testing.T) {
	cp := newControlPlane(t)
	cp.add("d1", map[string]string{"index.html": "one"})
	s := newSyncer(t, cp, nil)
	mustSync(t, s, cp.ref("d1"))
	blob := s.blobPath(sum("one"))

	// A kept version with no blob list uses no blobs, so they are collected.
	s.collectBlobs(map[string]bool{"ghost": true})
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Fatalf("an unreferenced blob survived: %v", err)
	}

	// An unreadable list is not "uses nothing": collecting then could delete a live blob.
	if err := os.WriteFile(blob, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	unreadable := filepath.Join(s.versionDir("d1"), blobsMarker)
	if err := os.Remove(unreadable); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	s.collectBlobs(map[string]bool{"d1": true})
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("a blob was collected although the blob list could not be read: %v", err)
	}
}

func TestRunAndSleepReturnAtOnceOnAnEndedContext(t *testing.T) {
	s := newSyncer(t, newControlPlane(t), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.sleep(ctx, 0) {
		t.Fatal("an untimed sleep kept waiting on an ended context")
	}
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return for an ended context")
	}
}

func TestCollectBlobsWithoutABlobsFolderDoesNothing(t *testing.T) {
	s := newSyncer(t, newControlPlane(t), nil)
	blobs := filepath.Join(s.cfg.Dir, "blobs")
	if err := os.RemoveAll(blobs); err != nil {
		t.Fatal(err)
	}
	s.collectBlobs(map[string]bool{})
	if _, err := os.Stat(blobs); !os.IsNotExist(err) {
		t.Fatalf("collecting created or changed the blobs folder: %v", err)
	}
}

func TestAnUntimedSleepEndsOnAWant(t *testing.T) {
	cp := newControlPlane(t)
	s := newSyncer(t, cp, nil)
	s.Want(cp.ref("d1"))
	if !s.sleep(context.Background(), 0) {
		t.Fatal("a Want did not end the untimed wait")
	}
}

func TestATimedSleepEndsWhenItsTimeIsUpAndOnAWant(t *testing.T) {
	cp := newControlPlane(t)
	s := newSyncer(t, cp, nil)
	if !s.sleep(context.Background(), time.Millisecond) {
		t.Fatal("a timed wait that ran out reported the context as ended")
	}
	s.Want(cp.ref("d1"))
	start := time.Now()
	if !s.sleep(context.Background(), time.Hour) || time.Since(start) > 5*time.Second {
		t.Fatal("a Want did not cut the timed wait short")
	}
}
