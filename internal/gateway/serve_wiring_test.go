package gateway

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/publicenv"
	"github.com/supatype/server/internal/stff"
	"github.com/supatype/server/internal/studioauth"
)

func TestForwardingFrontReportsAnAddressThatIsNotAnIP(t *testing.T) {
	hook := logtest.NewGlobal()
	t.Cleanup(hook.Reset)

	cfg := &conf.GlobalConfiguration{}
	cfg.Security.ForwardingSecret = "right"
	var decided string
	h := forwardingFront(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		decided, _ = stff.DecidedAddress(r)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.4.4.4:9"
	r.Header.Set(stff.SecretHeaderName, "right")
	r.Header.Set(stff.HeaderName, "not-an-ip")
	h.ServeHTTP(httptest.NewRecorder(), r)

	var warned bool
	for _, e := range hook.AllEntries() {
		warned = warned || (e.Level == logrus.WarnLevel && strings.Contains(e.Message, "error processing St-Forwarded-For"))
	}
	if !warned {
		t.Fatal("an St-Forwarded-For that is not an IP address was dropped without a warning")
	}
	if decided != "10.4.4.4" {
		t.Fatalf("decided = %q, want the raw peer when the forwarded address is unusable", decided)
	}
}

func TestNewSiteSyncerRefusesAConfigItCannotServe(t *testing.T) {
	cfg := managedAppConfig()
	cfg.SiteDir = ""
	if _, err := newSiteSyncer(&cfg, newLiveManifests(&proxy.RouteManifest{Schema: "public"})); err == nil {
		t.Fatal("a syncer was built with nowhere to keep the site")
	}
}

// With SUPATYPE_FUNCTIONS_LOG_TO_STDOUT set, a worker's output also reaches this server's stdout,
// which is where a container's logs are read from.
func TestFunctionsLogToStdoutReachesTheServersOutput(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(t.TempDir(), "entry.ts")
	if err := os.WriteFile(entry, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	installed(t, true)
	t.Setenv(fakeDenoEnv, "1")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	realStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = realStdout; _ = r.Close(); _ = w.Close() })

	manager := denoSupervisor(&config.Config{
		DenoPath:             self,
		DenoFunctionsDir:     filepath.Dir(entry),
		DenoServeScript:      entry,
		FunctionsLogToStdout: true,
	}, "")
	if manager == nil {
		t.Fatal("no supervisor")
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx)
	t.Cleanup(func() { cancel(); manager.Stop() })

	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "hello from the fake worker") {
				got <- sc.Text()
				return
			}
		}
	}()
	select {
	case line := <-got:
		if !strings.HasPrefix(line, "[") {
			t.Fatalf("line = %q, want the [function] prefix", line)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the worker's output never reached stdout")
	}
}

func TestTheStoredAdminConfigFallsBackToTheFileWhenThePushStateIsUnreadable(t *testing.T) {
	requireAuthEnvironment(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	d := depsFor(t, cfg, nil)
	stored := d.storedAdminConfig()
	if stored == nil {
		t.Skip("no admin database configured")
	}
	// The test database was never pushed to, so the stored read cannot succeed. What the admin
	// route serves then is the file, not a 500.
	dir := t.TempDir()
	t.Chdir(dir)
	file := "admin-config.json"
	if err := os.WriteFile(filepath.Join(dir, file), []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := stored(t.Context()); err == nil {
		t.Skip("the test database holds a pushed schema")
	}
	got, err := studioauth.ResolveAdminConfig(t.Context(), stored, file)
	if err != nil || string(got) != `{"from":"file"}` {
		t.Fatalf("got %q, %v; want the file's config", got, err)
	}
}

func managedAppEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"SUPATYPE_MODE":                "managed",
		"SUPATYPE_TENANT_HMAC_SECRET":  "tenant-secret",
		"SUPATYPE_SERVICE_ROLE_KEY":    "service-role",
		"SUPATYPE_KEYSPACE_ADDR":       requireKeyspace(t),
		"SUPATYPE_MANAGED_PROJECT_REF": "proj-app-listener",
		"SUPATYPE_APP_ADDR":            "127.0.0.1:0",
		"SUPATYPE_SITE_DIR":            t.TempDir(),
		"SUPATYPE_SITE_SOURCE_URL":     "http://127.0.0.1:1",
		"SUPATYPE_URL":                 "https://proj.example.test",
		"SUPATYPE_ANON_KEY":            "anon-key-for-the-test",
	}
}

// A managed project gateway with an app address hands back the app listener's handler: the public
// env routes answer, and with no site live everything else is a 404 rather than the API.
func TestNewWithAppHandsBackTheAppListener(t *testing.T) {
	requireAuthEnvironment(t)
	for k, v := range managedAppEnv(t) {
		t.Setenv(k, v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	handler, app, drain, err := NewWithApp(ctx)
	if err != nil {
		t.Fatalf("NewWithApp: %v", err)
	}
	t.Cleanup(func() { cancel(); drain() })
	if handler == nil || app == nil {
		t.Fatalf("handlers = %v, %v; want both", handler != nil, app != nil)
	}
	if rec := appRequest(app, http.MethodGet, "/some/page"); rec.Code != http.StatusNotFound {
		t.Errorf("/some/page with no site live = %d, want 404", rec.Code)
	}
	if rec := appRequest(app, http.MethodGet, publicenv.JSONPath); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Header().Get("Content-Type"), "json") || !strings.Contains(rec.Body.String(), "proj.example.test") {
		t.Errorf("%s = %d (%s), want the public env as JSON", publicenv.JSONPath, rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := appRequest(app, http.MethodGet, "/auth/v1/health"); rec.Code != http.StatusNotFound {
		t.Errorf("the API answered on the app listener: %d", rec.Code)
	}
}

func TestNewWithAppRefusesWhatTheAppListenerCannotRun(t *testing.T) {
	requireAuthEnvironment(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		managed bool
		env     map[string]string
		wantErr string
	}{
		"an app address on a gateway that is not a managed project": {
			false, map[string]string{"SUPATYPE_APP_ADDR": "127.0.0.1:0"}, "SUPATYPE_APP_ADDR"},
		"a site folder that cannot be made": {
			true, map[string]string{"SUPATYPE_SITE_DIR": filepath.Join(blocker, "site")}, "serve: site"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.managed {
				for k, v := range managedAppEnv(t) {
					t.Setenv(k, v)
				}
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			ctx, cancel := context.WithCancel(context.Background())
			_, _, drain, err := NewWithApp(ctx)
			cancel()
			if err == nil {
				drain()
				t.Fatal("started anyway")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}
