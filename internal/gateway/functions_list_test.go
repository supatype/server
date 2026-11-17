package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/functions"
	"github.com/supatype/server/internal/proxy"
)

const adminKey = "service-role-key"

// A worker that answers its own list and log routes and counts every request it gets.
func fakeWorker(t *testing.T, names ...string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/functions/v1/_functions":
			data := make([]map[string]string, 0, len(names))
			for _, n := range names {
				data = append(data, map[string]string{"name": n, "deployedAt": "2026-01-02T03:04:05Z"})
			}
			_ = json.NewEncoder(w).Encode(struct {
				Data []map[string]string `json:"data"`
			}{data})
		case "/functions/v1/_logs":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"msg\":\"hello\"}\n\n"))
		default:
			_, _ = w.Write([]byte("a function answered"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func adminDeps(cfg *config.Config, m *proxy.RouteManifest) *Deps {
	cfg.ServiceRoleKey = adminKey
	return &Deps{Config: cfg, ManifestFor: func(*http.Request) *proxy.RouteManifest { return m }}
}

func adminGet(t *testing.T, d *Deps, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	rec := httptest.NewRecorder()
	functionsAdminAPI(d).ServeHTTP(rec, req)
	return rec
}

func listedFrom(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []functions.FunctionMeta `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("list is not JSON: %v (%s)", err, rec.Body.String())
	}
	names := make([]string, 0, len(body.Data))
	for _, f := range body.Data {
		names = append(names, f.Name)
	}
	return names
}

func sameNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("listed %v, want %v", got, want)
	}
}

// The worker answers `_logs` and `_functions` for the server. Neither is a public endpoint: a caller
// holding only the anon key reached the log tail through /functions/v1/_logs.
func TestThePublicPathRefusesTheWorkersOwnRoutes(t *testing.T) {
	worker, hits := fakeWorker(t)
	h := functionsInvocationProxy(&config.Config{FunctionsWorkerURL: worker.URL}, functionsManifest, false)

	for _, path := range []string{
		"/_logs",
		"/_functions",
		"/_anything",
		"/functions/v1/_logs",
		"/functions/v1/_functions",
		"/hooks/audit",
		"/functions/v1/hooks/audit",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the worker was reached %d times by refused paths", n)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/send", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("a function was refused: %d", rec.Code)
	}
}

func functionsManifest(*http.Request) *proxy.RouteManifest {
	return &proxy.RouteManifest{}
}

// The admin API reaches the worker directly, so refusing the route publicly must not cut the tail.
func TestTheAdminLogTailStillReachesTheWorker(t *testing.T) {
	worker, hits := fakeWorker(t)
	d := adminDeps(&config.Config{FunctionsWorkerURL: worker.URL}, &proxy.RouteManifest{})

	rec := adminGet(t, d, "/logs/tail")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"msg":"hello"`) {
		t.Fatalf("tail = %d %q", rec.Code, rec.Body.String())
	}
	if hits.Load() != 1 {
		t.Fatalf("worker hits = %d, want 1", hits.Load())
	}

	// And the public proxy over the same worker still refuses it.
	pub := httptest.NewRecorder()
	functionsInvocationProxy(d.Config, d.ManifestFor, false).ServeHTTP(pub, httptest.NewRequest(http.MethodGet, "/_logs", nil))
	if pub.Code != http.StatusNotFound {
		t.Fatalf("public _logs = %d, want 404", pub.Code)
	}
}

func TestTheListComesFromTheProjectWorker(t *testing.T) {
	worker, _ := fakeWorker(t, "greet", "ping")
	// The server's own directory holds something else, which must not be listed.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stale.ts"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DenoFunctionsDir: dir}
	d := adminDeps(cfg, &proxy.RouteManifest{FunctionsWorkerURL: worker.URL})

	sameNames(t, listedFrom(t, adminGet(t, d, "/list")), "greet", "ping")
}

func TestTheListComesFromTheConfiguredWorkerWhenTheManifestNamesNone(t *testing.T) {
	worker, _ := fakeWorker(t, "greet")
	d := adminDeps(&config.Config{FunctionsWorkerURL: worker.URL}, &proxy.RouteManifest{})

	sameNames(t, listedFrom(t, adminGet(t, d, "/list")), "greet")
}

func TestThePerFunctionWorkersContributeTheirNamesWithoutHooks(t *testing.T) {
	worker, hits := fakeWorker(t)
	m := &proxy.RouteManifest{FunctionWorkerURLs: map[string]string{
		"send":        worker.URL,
		"greet":       worker.URL,
		"hooks/audit": worker.URL,
	}}
	d := adminDeps(&config.Config{}, m)

	sameNames(t, listedFrom(t, adminGet(t, d, "/list")), "greet", "send")
	if hits.Load() != 0 {
		t.Fatalf("a per-function worker was asked %d times; its name is already known", hits.Load())
	}
}

func TestPerFunctionAndProjectWorkersListTogether(t *testing.T) {
	worker, _ := fakeWorker(t, "greet", "ping")
	m := &proxy.RouteManifest{
		FunctionsWorkerURL: worker.URL,
		FunctionWorkerURLs: map[string]string{"ping": "http://fn-ping.svc:8001", "send": "http://fn-send.svc:8001"},
	}
	d := adminDeps(&config.Config{}, m)

	sameNames(t, listedFrom(t, adminGet(t, d, "/list")), "greet", "ping", "send")
}

func TestWithNoWorkerTheServersOwnDirectoryIsListed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "local.ts"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := adminDeps(&config.Config{DenoFunctionsDir: dir}, &proxy.RouteManifest{})

	sameNames(t, listedFrom(t, adminGet(t, d, "/list")), "local")
}

// An empty list reads as "no functions deployed", which is false when the worker is merely down.
func TestAWorkerThatCannotBeAskedFailsTheListInsteadOfEmptyingIt(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer broken.Close()
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer garbled.Close()

	for name, url := range map[string]string{"unreachable": downURL, "answers 500": broken.URL, "answers garbage": garbled.URL} {
		d := adminDeps(&config.Config{}, &proxy.RouteManifest{FunctionsWorkerURL: url})
		rec := adminGet(t, d, "/list")
		if rec.Code != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502 (%s)", name, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"data"`) {
			t.Errorf("%s: answered a list: %s", name, rec.Body.String())
		}
	}
}

// The list call goes to a worker that may be compromised or misconfigured, and carries the server's
// network position with it: a redirect it answers must not be followed to somewhere else.
func TestTheWorkerListCallDoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"name":"stolen"}]}`))
	}))
	defer target.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/functions/v1/_functions", http.StatusFound)
	}))
	defer redirecting.Close()

	d := adminDeps(&config.Config{}, &proxy.RouteManifest{FunctionsWorkerURL: redirecting.URL})
	rec := adminGet(t, d, "/list")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (%s)", rec.Code, rec.Body.String())
	}
	if n := targetHits.Load(); n != 0 {
		t.Errorf("the redirect target was asked %d times; a redirect must not be followed", n)
	}
	if strings.Contains(rec.Body.String(), "stolen") {
		t.Errorf("the redirect target's list was served: %s", rec.Body.String())
	}
}

// A list is a few hundred bytes. A worker that streams without end must not be read without end.
func TestTheWorkerListAnswerIsCapped(t *testing.T) {
	var sent atomic.Int64
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"name":"a"}],"pad":"`))
		chunk := []byte(strings.Repeat("x", 64*1024))
		for i := 0; i < 256; i++ { // 16 MiB if it is all read
			n, err := w.Write(chunk)
			sent.Add(int64(n))
			if err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	defer huge.Close()

	d := adminDeps(&config.Config{}, &proxy.RouteManifest{FunctionsWorkerURL: huge.URL})
	rec := adminGet(t, d, "/list")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 for an answer over the cap (%s)", rec.Code, rec.Body.String())
	}
	if n := sent.Load(); n > 4<<20 {
		t.Errorf("the worker was read for %d bytes; the cap is %d", n, maxWorkerListBytes)
	}
}

// The worker's address is where it sits on the project network. A caller of the admin API has no
// need of it, and a failed list must not hand it over.
func TestTheWorkerAddressIsNotInAnError(t *testing.T) {
	const secretHost = "worker-secret.internal"
	_, err := askWorker(t.Context(), &http.Client{}, "http://"+secretHost+":99999\x7f")
	if err == nil {
		t.Fatal("a malformed worker address did not fail")
	}
	if strings.Contains(err.Error(), secretHost) {
		t.Errorf("the error carries the worker address: %v", err)
	}

	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()
	d := adminDeps(&config.Config{}, &proxy.RouteManifest{FunctionsWorkerURL: downURL})
	rec := adminGet(t, d, "/list")
	if strings.Contains(rec.Body.String(), strings.TrimPrefix(downURL, "http://")) {
		t.Errorf("the 502 body carries the worker address: %s", rec.Body.String())
	}
}

// With no worker to ask, a functions directory that cannot be read fails the list rather than
// emptying it: an empty list reads as "no functions deployed".
func TestAnUnreadableFunctionsDirectoryFailsTheList(t *testing.T) {
	// A NUL in the path is refused by the operating system on every platform, and is not the
	// "does not exist" that lists as empty.
	unreadable := t.TempDir() + "\x00"
	d := adminDeps(&config.Config{DenoFunctionsDir: unreadable}, &proxy.RouteManifest{})

	rec := adminGet(t, d, "/list")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"data"`) {
		t.Errorf("answered a list: %s", rec.Body.String())
	}
}
