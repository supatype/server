package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/auth"
	"github.com/supatype/server/internal/auth/apiworker"
	"github.com/supatype/server/internal/auth/mailer/templatemailer"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/reloader"
)

// New is the whole bootstrap: configuration, the database, the Deno supervisor,
// the caches, and the assembled mux. Nothing exercised it, so a change that
// broke startup would have been found by running the binary.
//
// It needs the same environment the auth service's own tests need: hack/test.env
// and a migrated database.

// authTestConfig is loaded rather than assumed to be exported. CI runs the
// suite without it in the environment, so every database-backed test in this
// repository loads it for itself; reading the environment instead passed
// locally, skipped every one of these on CI, and left the package 23 points
// short of the floor with nothing failing to say why.
const authTestConfig = "../../hack/test.env"

func requireAuthEnvironment(t *testing.T) {
	t.Helper()

	cfg, err := conf.LoadGlobal(authTestConfig)
	if err != nil {
		t.Skipf("the auth configuration does not load: %v", err)
	}
	// New waits for a database that is merely not up yet, which is what it is
	// supposed to do and what makes a run with no database hang until the go
	// test deadline rather than say why. Ask once, briefly, and skip instead.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := storage.DialContext(ctx, cfg)
	if err != nil {
		t.Skipf("the database is not reachable: %v", err)
	}
	_ = db.Close()
}

// The server starts, serves, and gives back everything it took.
func TestNewBuildsAServerThatServes(t *testing.T) {
	requireAuthEnvironment(t)

	ctx, cancel := context.WithCancel(context.Background())
	handler, drain, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if handler == nil || drain == nil {
		t.Fatal("New returned nothing to serve or nothing to drain")
	}

	// Health is mounted on the outer mux and answers without any upstream being
	// reachable, which is what makes it usable as a liveness probe.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/health: status = %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "version") {
		t.Errorf("/health body = %s", rec.Body.String())
	}

	// The auth service is behind /auth/v1.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/auth/v1/health: status = %d (%s)", rec.Code, rec.Body.String())
	}

	// Cancelling the context stops the workers, and draining waits for them.
	cancel()
	done := make(chan struct{})
	go func() {
		drain()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("drain did not return")
	}
}

// A configuration file that is not there is a reason to refuse to start: a
// server told to read one and silently given the environment instead is a
// deployment running on something other than what was asked for.
func TestNewRefusesAConfigFileThatIsNotThere(t *testing.T) {
	requireAuthEnvironment(t)

	previous := ConfigFile
	t.Cleanup(func() { ConfigFile = previous })
	ConfigFile = "no-such-config.env"

	if _, _, err := New(context.Background()); err == nil {
		t.Error("want an error")
	}
}

// A watch directory that is not there is a warning, not a refusal: it is where
// a platform drops optional overrides, and most deployments have none.
func TestNewToleratesAWatchDirectoryThatIsNotThere(t *testing.T) {
	requireAuthEnvironment(t)

	previous := WatchDir
	t.Cleanup(func() { WatchDir = previous })
	WatchDir = "no-such-directory"

	ctx, cancel := context.WithCancel(context.Background())
	handler, drain, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if handler == nil {
		t.Fatal("no handler")
	}
	cancel()
	drain()
}

// ─── What the bootstrap refuses ───────────────────────────────────────────────

// Every reason New declines to start. A server that started anyway with one of
// these missing would answer requests it cannot serve correctly: managed mode
// with no tenant secret trusts any X-Supatype-Tenant header it is sent.
func TestNewRefuses(t *testing.T) {
	requireAuthEnvironment(t)

	manifest := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifest, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, env := range map[string]map[string]string{
		"managed mode with no tenant secret": {
			"SUPATYPE_MODE":               "managed",
			"SUPATYPE_TENANT_HMAC_SECRET": "",
			"SUPATYPE_SERVICE_ROLE_KEY":   "service-role",
		},
		"a mode other than dev with no service role key": {
			"SUPATYPE_MODE":             "standalone",
			"SUPATYPE_SERVICE_ROLE_KEY": "",
		},
		"a route manifest that is not JSON": {
			"SUPATYPE_MANIFEST_PATH": manifest,
		},
		"a configuration value that is not a boolean": {
			"SUPATYPE_APP_SPA_FALLBACK": "maybe",
		},
		"an auth configuration that will not decode": {
			"SUPATYPE_HOOK_SEND_EMAIL_ENABLED": "maybe",
		},
		"a database that refuses the credential": {
			"DATABASE_URL": "postgresql://postgres:not-the-password@127.0.0.1:5432/supatype_studio_test",
		},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			ctx, cancel := context.WithCancel(context.Background())
			handler, drain, err := New(ctx)
			cancel()
			if err == nil {
				drain()
				t.Fatalf("started anyway (handler = %v)", handler != nil)
			}
		})
	}
}

// ─── What the bootstrap builds ────────────────────────────────────────────────

// requireValkey skips unless there is a Valkey to run the managed-mode paths
// against. The route manifest for a managed pod comes from it, so there is
// nothing to assert without one.
func requireValkey(t *testing.T) string {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("SUPATYPE_TEST_VALKEY_ADDR"))
	if addr == "" {
		t.Skip("SUPATYPE_TEST_VALKEY_ADDR is not set")
	}
	return addr
}

// servesHealth starts a server over this environment and returns what /health
// answered, so each bootstrap variation is checked by serving rather than by
// New merely returning.
func servesHealth(t *testing.T, env map[string]string, header http.Header) int {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}

	ctx, cancel := context.WithCancel(context.Background())
	handler, drain, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		drain()
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	for k, values := range header {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

// A managed pod for one project merges the manifest the control plane published
// over its own file.
func TestNewMergesTheManagedManifest(t *testing.T) {
	requireAuthEnvironment(t)
	addr := requireValkey(t)

	if got := servesHealth(t, map[string]string{
		"SUPATYPE_MODE":                "managed",
		"SUPATYPE_TENANT_HMAC_SECRET":  "tenant-secret",
		"SUPATYPE_SERVICE_ROLE_KEY":    "service-role",
		"SUPATYPE_VALKEY_ADDR":         addr,
		"SUPATYPE_MANAGED_PROJECT_REF": "proj-bootstrap-test",
	}, nil); got != http.StatusOK {
		t.Errorf("/health: status = %d", got)
	}
}

// A managed pod with no project ref of its own serves many, resolving each
// caller's manifest from the tenant header. Health is answered before that
// lookup, which is what makes it usable as the pod's readiness probe.
func TestNewServesManyTenants(t *testing.T) {
	requireAuthEnvironment(t)
	addr := requireValkey(t)

	header := http.Header{}
	header.Set("X-Supatype-Tenant", "proj-some-tenant")

	if got := servesHealth(t, map[string]string{
		"SUPATYPE_MODE":                "managed",
		"SUPATYPE_TENANT_HMAC_SECRET":  "tenant-secret",
		"SUPATYPE_SERVICE_ROLE_KEY":    "service-role",
		"SUPATYPE_VALKEY_ADDR":         addr,
		"SUPATYPE_MANAGED_PROJECT_REF": "",
	}, header); got != http.StatusOK {
		t.Errorf("/health: status = %d", got)
	}
}

// An external functions worker takes the place of the in-process Deno, and a
// Deno that is not installed disables invocations rather than refusing to
// start: the rest of the stack is still worth serving.
func TestNewWithoutAnInProcessDeno(t *testing.T) {
	requireAuthEnvironment(t)

	for name, env := range map[string]map[string]string{
		"an external worker": {
			"SUPATYPE_FUNCTIONS_WORKER_URL": "http://functions-worker:8000",
		},
		"no Deno on PATH": {
			"SUPATYPE_FUNCTIONS_WORKER_URL": "",
			"SUPATYPE_DENO_PATH":            "deno-that-is-not-installed",
			"SUPATYPE_DENO_FUNCTIONS_DIR":   t.TempDir(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := servesHealth(t, env, nil); got != http.StatusOK {
				t.Errorf("/health: status = %d", got)
			}
		})
	}
}

// The send-email hook receiver is mounted only when the auth service is
// configured to POST to it. Without the mount the auth service's own sends
// would 404 and no mail would go out at all.
func TestNewMountsTheSendEmailHookReceiver(t *testing.T) {
	requireAuthEnvironment(t)

	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	t.Setenv("SUPATYPE_HOOK_SEND_EMAIL_ENABLED", "true")
	t.Setenv("SUPATYPE_HOOK_SEND_EMAIL_URI", "http://localhost:9999/internal/v0hooks/send-email")
	t.Setenv("SUPATYPE_HOOK_SEND_EMAIL_SECRETS", "v1,"+secret)

	ctx, cancel := context.WithCancel(context.Background())
	handler, drain, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		drain()
	})

	// Unsigned, so it is refused — the point is that something refused it rather
	// than the route being absent.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(
		http.MethodPost, "/internal/v0hooks/send-email", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (%s)", rec.Code, rec.Body.String())
	}
}

// A .env file that will not parse is a warning, not a refusal. The values it
// would have set are missing, which the configuration checks below catch on
// their own terms; refusing here would say nothing about which line is wrong.
func TestNewToleratesADotEnvThatWillNotParse(t *testing.T) {
	requireAuthEnvironment(t)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.env"), []byte("# nothing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("this line has no equals sign\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	previous := ConfigFile
	t.Cleanup(func() { ConfigFile = previous })
	ConfigFile = filepath.Join(dir, "server.env")

	if got := servesHealth(t, nil, nil); got != http.StatusOK {
		t.Errorf("/health: status = %d", got)
	}
}

// A working directory that cannot be named is the same: there are no .env files
// to find relative to it, and that is all it means.
func TestNewToleratesAWorkingDirectoryItCannotName(t *testing.T) {
	requireAuthEnvironment(t)

	previous := getwd
	t.Cleanup(func() { getwd = previous })
	getwd = func() (string, error) { return "", errors.New("the working directory is gone") }

	if got := servesHealth(t, nil, nil); got != http.StatusOK {
		t.Errorf("/health: status = %d", got)
	}
}

// A managed deployment that cannot reach Valkey refuses to start. Its route
// manifests and tenant configuration come from there, so carrying on would mean
// serving every tenant the file's defaults.
func TestNewRefusesAManagedPodWithNoValkey(t *testing.T) {
	requireAuthEnvironment(t)

	t.Setenv("SUPATYPE_MODE", "managed")
	t.Setenv("SUPATYPE_TENANT_HMAC_SECRET", "tenant-secret")
	t.Setenv("SUPATYPE_SERVICE_ROLE_KEY", "service-role")
	t.Setenv("SUPATYPE_VALKEY_ADDR", "127.0.0.1:59999")

	ctx, cancel := context.WithCancel(context.Background())
	_, drain, err := New(ctx)
	cancel()
	if err == nil {
		drain()
		t.Fatal("started without the Valkey it reads its tenants from")
	}
}

// A manifest in a directory that is not there cannot be watched, and the server
// serves the manifest it already has rather than refusing to start.
func TestNewToleratesAManifestItCannotWatch(t *testing.T) {
	requireAuthEnvironment(t)

	if got := servesHealth(t, map[string]string{
		"SUPATYPE_MANIFEST_PATH": filepath.Join(t.TempDir(), "no-such-directory", "manifest.json"),
	}, nil); got != http.StatusOK {
		t.Errorf("/health: status = %d", got)
	}
}

// ─── The database Studio and the SQL runner are given ─────────────────────────

// Both read through the connections the gateway already holds rather than
// opening ones of their own. A Deps that answered no pool would take the SQL
// runner and every identity-scoped table down with it.
func TestDepsHandOverTheAdminPool(t *testing.T) {
	requireAuthEnvironment(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SQLDSN() == "" {
		t.Skip("no SQL DSN configured")
	}
	d := depsFor(t, cfg, nil)

	pool, err := d.AdminPool()
	if err != nil {
		t.Fatalf("AdminPool: %v", err)
	}
	if pool == nil {
		t.Fatal("no pool")
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Errorf("the pool does not reach the database: %v", err)
	} else {
		_ = tx.Rollback(t.Context())
	}

	// Identity-scoped tables are read from the same connection. A project that
	// has never pushed a schema has none, which is not an error.
	if _, ok := d.IdentityScopedTables(t.Context()); !ok {
		t.Log("no identity-scoped tables for this project")
	}
}

// ─── Delivering a send-email hook ─────────────────────────────────────────────

// A signed payload reaches the auth service. What it answers depends on the
// payload; what matters here is that the receiver verified it, decoded it, and
// handed it to something that could act on it, rather than reporting a
// misconfigured server.
func TestTheSendEmailHookReachesTheAuthService(t *testing.T) {
	requireAuthEnvironment(t)

	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	t.Setenv("SUPATYPE_HOOK_SEND_EMAIL_ENABLED", "true")
	t.Setenv("SUPATYPE_HOOK_SEND_EMAIL_URI", "http://localhost:9999/internal/v0hooks/send-email")
	t.Setenv("SUPATYPE_HOOK_SEND_EMAIL_SECRETS", "v1,"+secret)

	ctx, cancel := context.WithCancel(context.Background())
	handler, drain, err := New(ctx)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		drain()
	})

	for name, payload := range map[string]string{
		"a payload with no user": `{"email_data":{"site_url":"http://localhost:9999"}}`,
		"a payload with a user": `{"user":{"id":"00000000-0000-0000-0000-000000000000",` +
			`"email":"nobody@example.com"},"email_data":{"email_action_type":"signup",` +
			`"site_url":"http://localhost:9999","token":"123456","token_hash":"h"}}`,
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, signedRequest(t, secret, []byte(payload)))

		if rec.Code == http.StatusInternalServerError &&
			strings.Contains(rec.Body.String(), "misconfigured server") {
			t.Errorf("%s: the receiver could not find the auth service", name)
		}
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s: the signature was refused", name)
		}
		t.Logf("%s: status = %d (%s)", name, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// ─── Reloading the configuration under a running server ───────────────────────

// A platform drops updated files into the watch directory rather than restarting
// the pod. The handler every request goes through has to become the new one, or
// the edit is accepted and then ignored.
func TestReloadingSwapsTheServedAPI(t *testing.T) {
	requireAuthEnvironment(t)

	authCfg, err := conf.LoadGlobalFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.DialContext(t.Context(), authCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	templates := templatemailer.NewCache()
	before := auth.NewAPIWithVersion(authCfg, db, "before", auth.WithMailer(templatemailer.FromConfig(authCfg, templates)))
	ah := reloader.NewAtomicHandler(before)

	r := &apiReloader{
		handler:   ah,
		worker:    apiworker.New(authCfg, templates, db, logrus.WithField("component", "test")),
		db:        db,
		templates: templates,
		limiters:  auth.NewLimiterOptions(authCfg),
	}
	r.Apply(authCfg)

	if ah.LoadHandler() == http.Handler(before) {
		t.Error("the served API was not swapped")
	}
	if _, ok := ah.LoadHandler().(*auth.API); !ok {
		t.Errorf("what was stored is not the auth service: %T", ah.LoadHandler())
	}
}

// A deployment with Deno installed runs edge functions in a process of its own,
// and gives it back when the server drains. CI has no Deno, so whether it is
// installed is the seam: without it this branch of the bootstrap is only ever
// exercised on a developer's machine.
func TestNewRunsDenoInProcessWhenItIsInstalled(t *testing.T) {
	requireAuthEnvironment(t)

	previous := lookPath
	t.Cleanup(func() { lookPath = previous })
	// Reported as installed, but the supervisor still cannot run it, so no
	// subprocess is created. What is under test is that the server built one,
	// started it, and stopped it.
	lookPath = func(file string) (string, error) { return file, nil }

	if got := servesHealth(t, map[string]string{
		"SUPATYPE_FUNCTIONS_WORKER_URL": "",
		"SUPATYPE_DENO_FUNCTIONS_DIR":   t.TempDir(),
		"SUPATYPE_DENO_PATH":            "deno",
	}, nil); got != http.StatusOK {
		t.Errorf("/health: status = %d", got)
	}
}
