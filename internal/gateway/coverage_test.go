package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
	"github.com/supatype/server/internal/modelhooks"
	"github.com/supatype/server/internal/proxy"
)

// The parts of the gateway that decide what a request is told when an upstream
// cannot be worked out, what headers a proxied request carries, and what the
// access log records.

// depsFor builds a Deps over this configuration and manifest.
func depsFor(t *testing.T, cfg *config.Config, manifest *proxy.RouteManifest) *Deps {
	t.Helper()
	if manifest == nil {
		manifest = &proxy.RouteManifest{Schema: "public"}
	}
	resources, err := data.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resources.Close() })

	return NewDeps(
		cfg,
		func(*http.Request) *proxy.RouteManifest { return manifest },
		nil,
		http.NotFoundHandler(),
		nil,
		"test",
		resources,
		http.NotFoundHandler(),
	)
}

// ─── The per-mode middleware stack ────────────────────────────────────────────

// Standalone puts the configured allowlist in front of the mux, and nothing at
// all when there is no allowlist to enforce.
func TestTheStandaloneChain(t *testing.T) {
	withOrigins := ModeChain(&config.Config{Mode: "standalone", CorsAllowOrigins: "https://app.example"}, nil)
	if len(withOrigins) != 1 {
		t.Fatalf("with an allowlist: %d middleware, want one", len(withOrigins))
	}

	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	withOrigins.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("allow-origin = %q, want the configured origin reflected", got)
	}

	if got := ModeChain(&config.Config{Mode: "standalone"}, nil); len(got) != 0 {
		t.Errorf("with no allowlist: %d middleware, want none", len(got))
	}
}

// Managed mode without a JWT secret still builds its chain — refusing to start
// would take out a pod for a misconfiguration the data plane already handles by
// refusing requests — but the chain is the same shape either way.
func TestTheManagedChainWithoutASecret(t *testing.T) {
	withSecret := ModeChain(&config.Config{Mode: "managed", JWTSecret: "s"}, nil)
	without := ModeChain(&config.Config{Mode: "managed"}, nil)

	if len(withSecret) != len(without) {
		t.Errorf("chains differ in length: %d and %d", len(withSecret), len(without))
	}
}

// ─── What the mounts derive per request ───────────────────────────────────────

// The schema a REST request resolves to: the admin API's override if one is
// set, otherwise the tenant manifest's, otherwise public.
func TestRestSchema(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Mode: "standalone", ApiConfigPath: filepath.Join(dir, "api.json")}

	fromManifest := depsFor(t, cfg, &proxy.RouteManifest{Schema: "tenant"})
	req := httptest.NewRequest(http.MethodGet, "/posts", nil)
	if got := fromManifest.RestSchema(req); got != "tenant" {
		t.Errorf("schema = %q, want the manifest's", got)
	}

	// A manifest that names none falls back to public.
	blank := depsFor(t, cfg, &proxy.RouteManifest{})
	if got := blank.RestSchema(req); got != "public" {
		t.Errorf("schema = %q, want public", got)
	}

	// And the admin API's override wins over both.
	api := apiconfig.DefaultApiConfig()
	api.Rest.Schema = "override"
	if err := fromManifest.APIStore.Set(t.Context(), api); err != nil {
		t.Fatal(err)
	}
	if got := fromManifest.RestSchema(req); got != "override" {
		t.Errorf("schema = %q, want the admin override", got)
	}
}

// The row cap is sent only when it differs from the default, so an unchanged
// configuration adds no header at all.
func TestRestMaxRows(t *testing.T) {
	dir := t.TempDir()
	d := depsFor(t, &config.Config{Mode: "standalone", ApiConfigPath: filepath.Join(dir, "api.json")}, nil)
	req := httptest.NewRequest(http.MethodGet, "/posts", nil)

	if got := d.RestMaxRows(req); got != "" {
		t.Errorf("with nothing configured: %q, want none", got)
	}

	api := apiconfig.DefaultApiConfig()
	api.Rest.MaxRows = 25
	if err := d.APIStore.Set(t.Context(), api); err != nil {
		t.Fatal(err)
	}
	if got := d.RestMaxRows(req); got != "25" {
		t.Errorf("with a cap: %q, want 25", got)
	}

	// The default is not worth a header.
	api.Rest.MaxRows = apiconfig.DefaultApiConfig().Rest.MaxRows
	if err := d.APIStore.Set(t.Context(), api); err != nil {
		t.Fatal(err)
	}
	if got := d.RestMaxRows(req); got != "" {
		t.Errorf("with the default: %q, want none", got)
	}
}

// A configuration that cannot be read adds no header rather than guessing one.
func TestRestValuesWithAnUnreadableConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "api.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := depsFor(t, &config.Config{Mode: "standalone", ApiConfigPath: path}, &proxy.RouteManifest{Schema: "tenant"})
	req := httptest.NewRequest(http.MethodGet, "/posts", nil)

	if got := d.RestMaxRows(req); got != "" {
		t.Errorf("max rows = %q, want none", got)
	}
	if got := d.RestSchema(req); got != "tenant" {
		t.Errorf("schema = %q, want the manifest's", got)
	}
}

// A deployment with no database has no admin pool, and the SQL runner is told
// so rather than handed a nil it would dereference.
func TestAdminPoolWithoutADatabase(t *testing.T) {
	d := depsFor(t, &config.Config{Mode: "standalone"}, nil)

	pool, err := d.AdminPool()
	if err == nil {
		t.Error("want an error")
	}
	if pool != nil {
		t.Errorf("pool = %#v, want a nil interface", pool)
	}
}

// ─── The GraphQL headers ──────────────────────────────────────────────────────

// The proxy authenticates as the service role and forwards the caller's own
// token separately, so pg_graphql sees both. An unconfigured service role sends
// neither, rather than an empty bearer.
func TestGraphQLHeaders(t *testing.T) {
	for name, tc := range map[string]struct {
		key, endUser string
		wantAuth     string
		wantEndUser  string
	}{
		"a bare key": {"service-key", "Bearer caller", "Bearer service-key", "Bearer caller"},
		"a key that already says bearer": {
			"Bearer service-key", "Bearer caller", "Bearer service-key", "Bearer caller",
		},
		"oddly cased":              {"BEARER service-key", "", "BEARER service-key", ""},
		"no caller token":          {"service-key", "", "Bearer service-key", ""},
		"no service role":          {"", "Bearer caller", "", ""},
		"a service role of spaces": {"   ", "Bearer caller", "", ""},
	} {
		headers := graphQLHeaders(tc.key, tc.endUser)
		if got := headers["Authorization"]; got != tc.wantAuth {
			t.Errorf("%s: Authorization = %q, want %q", name, got, tc.wantAuth)
		}
		if got := headers["X-Supatype-End-User-Authorization"]; got != tc.wantEndUser {
			t.Errorf("%s: end-user header = %q, want %q", name, got, tc.wantEndUser)
		}
	}
}

// The GraphQL mount rewrites to the RPC endpoint on a copy, so the caller's own
// request is untouched.
func TestRewriteToGraphQLRPC(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/graphql/v1?op=x", strings.NewReader(`{}`))

	rpc := rewriteToGraphQLRPC(req)
	if rpc.URL.Path != "/rpc/graphql" {
		t.Errorf("path = %q", rpc.URL.Path)
	}
	if rpc.URL.RawQuery != "op=x" {
		t.Errorf("the query was lost: %q", rpc.URL.RawQuery)
	}
	if rpc.Header.Get("Content-Profile") != "graphql_public" {
		t.Errorf("content-profile = %q", rpc.Header.Get("Content-Profile"))
	}
	if req.URL.Path != "/graphql/v1" {
		t.Errorf("the original request was rewritten: %q", req.URL.Path)
	}
}

// ─── The admin config endpoint ────────────────────────────────────────────────

// Studio reads the pushed admin config from here. A project that has not pushed
// is a 404 — there is nothing to render — and anything else is a failure to
// read, which is a different problem.
func TestTheStudioConfigEndpoint(t *testing.T) {
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	// Dev bypass, so the admission this endpoint sits behind is not what is
	// under test here.
	open := &config.Config{
		Mode: "dev", AdminConfigPath: "admin-config.json",
		StudioOpenDev: config.SwitchBool(true),
	}
	notPushed := depsFor(t, open, nil)
	rec := httptest.NewRecorder()
	buildStudioConfig(notPushed).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/studio/config", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("nothing pushed: status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}

	// Pushed.
	if err := os.WriteFile("admin-config.json", []byte(`{"adminRoles":["owner"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	buildStudioConfig(notPushed).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/studio/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pushed: status = %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q", got)
	}
	if !strings.Contains(rec.Body.String(), "owner") {
		t.Errorf("body = %s", rec.Body.String())
	}

	// A path that cannot be read at all is not a project that has not pushed.
	unreadable := depsFor(t, &config.Config{
		Mode: "dev", AdminConfigPath: "..", StudioOpenDev: config.SwitchBool(true),
	}, nil)
	rec = httptest.NewRecorder()
	buildStudioConfig(unreadable).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/studio/config", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("unreadable: status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

// ─── Upstreams that cannot be worked out ──────────────────────────────────────

// An upstream URL that will not parse is a bad gateway. Proxying to it would be
// worse: the request would go somewhere nobody chose.
func TestAnUpstreamThatWillNotParse(t *testing.T) {
	cfg := &config.Config{
		Mode:         "standalone",
		PostgRESTURL: "://not a url",
		GraphQLURL:   "://not a url",
	}
	d := depsFor(t, cfg, &proxy.RouteManifest{Schema: "public"})

	for name, handler := range map[string]http.Handler{
		"rest":    restProxyHandler(d),
		"graphql": buildGraphQL(d),
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/posts", nil))
		if rec.Code != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502", name, rec.Code)
		}
	}
}

func TestItoa(t *testing.T) {
	if got := itoa(25); got != "25" {
		t.Errorf("itoa(25) = %q", got)
	}
}

// ─── The access log ───────────────────────────────────────────────────────────

// The tenant a request is logged against: the routed header first, then the
// deployment's own project ref in managed mode, and nothing at all otherwise.
func TestTenantForAccessLog(t *testing.T) {
	withContext := func(mode, ref, header string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
		if header != "" {
			req.Header.Set("X-Supatype-Tenant", header)
		}
		var captured *http.Request
		WithOuterAccessLogContext(mode, ref)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			captured = r
		})).ServeHTTP(httptest.NewRecorder(), req)
		return captured
	}

	for name, tc := range map[string]struct {
		mode, ref, header string
		want              string
	}{
		"the routed tenant":            {"managed", "proj-1", "routed", "routed"},
		"the deployment's own project": {"managed", "proj-1", "", "proj-1"},
		"managed with no project":      {"managed", "", "", ""},
		"a header outside managed":     {"standalone", "", "routed", "routed"},
		"standalone with nothing":      {"standalone", "", "", ""},
		"a header of spaces":           {"managed", "proj-1", "   ", "proj-1"},
	} {
		if got := tenantForAccessLog(withContext(tc.mode, tc.ref, tc.header)); got != tc.want {
			t.Errorf("%s: tenant = %q, want %q", name, got, tc.want)
		}
	}

	// Nothing to read from is nothing, rather than a dereference.
	if got := tenantForAccessLog(nil); got != "" {
		t.Errorf("no request: %q", got)
	}
	if got := outerLogMode(nil); got != "" {
		t.Errorf("outerLogMode(nil) = %q", got)
	}
	if got := outerLogManagedProjectRef(nil); got != "" {
		t.Errorf("outerLogManagedProjectRef(nil) = %q", got)
	}
}

// A level nobody configured, or one nobody recognises, is info: a log that
// stopped recording because of a typo is worse than a noisy one.
func TestTheAccessLogLevel(t *testing.T) {
	for name, tc := range map[string]struct {
		level string
		want  logrus.Level
	}{
		"nothing":         {"", logrus.InfoLevel},
		"only whitespace": {"   ", logrus.InfoLevel},
		"nonsense":        {"chatty", logrus.InfoLevel},
		"warn":            {"warn", logrus.WarnLevel},
		"debug":           {"debug", logrus.DebugLevel},
	} {
		if got := newOuterAccessLogger(&bytes.Buffer{}, tc.level).GetLevel(); got != tc.want {
			t.Errorf("%s: level = %v, want %v", name, got, tc.want)
		}
	}
}

// One JSON line per request, carrying what an operator needs to find it again.
func TestTheAccessLogLine(t *testing.T) {
	var out bytes.Buffer
	logger := newOuterAccessLogger(&out, "debug")

	outerAccessMu.Lock()
	previous := outerAccessLogger
	outerAccessLogger = logger
	outerAccessMu.Unlock()
	t.Cleanup(func() {
		outerAccessMu.Lock()
		outerAccessLogger = previous
		outerAccessMu.Unlock()
	})

	req := httptest.NewRequest(http.MethodPost, "/rest/v1/posts?select=id", nil)
	req.Header.Set("X-Supatype-Tenant", "tenant-1")
	entry := outerAccessLogFormatter{}.NewLogEntry(req)
	entry.Write(http.StatusCreated, 42, nil, 3*time.Millisecond, nil)

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &line); err != nil {
		t.Fatalf("not one JSON line: %q", out.String())
	}
	for _, field := range []string{"component", "method", "path", "status", "duration_ms", "tenant"} {
		if _, present := line[field]; !present {
			t.Errorf("%s is missing from %v", field, line)
		}
	}
	if line["method"] != http.MethodPost || line["path"] != "/rest/v1/posts" {
		t.Errorf("line = %v", line)
	}
	if line["tenant"] != "tenant-1" {
		t.Errorf("tenant = %v", line["tenant"])
	}
}

// Health checks are logged at debug, so the default level is not buried in
// probe traffic.
func TestHealthChecksAreLoggedQuietly(t *testing.T) {
	var out bytes.Buffer
	logger := newOuterAccessLogger(&out, "info")

	outerAccessMu.Lock()
	previous := outerAccessLogger
	outerAccessLogger = logger
	outerAccessMu.Unlock()
	t.Cleanup(func() {
		outerAccessMu.Lock()
		outerAccessLogger = previous
		outerAccessMu.Unlock()
	})

	for _, path := range []string{"/health", "/health/ready"} {
		outerAccessLogFormatter{}.NewLogEntry(httptest.NewRequest(http.MethodGet, path, nil)).
			Write(http.StatusOK, 10, nil, time.Millisecond, nil)
	}
	if out.Len() != 0 {
		t.Errorf("health checks were logged at info: %s", out.String())
	}

	// And an ordinary request still is.
	outerAccessLogFormatter{}.NewLogEntry(httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)).
		Write(http.StatusOK, 10, nil, time.Millisecond, nil)
	if out.Len() == 0 {
		t.Error("an ordinary request was not logged")
	}
}

// A log entry with no request cannot describe one, and must not panic trying.
func TestALogEntryWithNoRequest(t *testing.T) {
	entry := &outerLogEntry{}
	entry.Write(http.StatusOK, 0, nil, time.Millisecond, nil)
	entry.Panic("boom", []byte("stack"))
}

// A panic is recorded with the request id, so the line can be tied to the
// request that caused it.
func TestAPanicIsLogged(t *testing.T) {
	var out bytes.Buffer
	logger := newOuterAccessLogger(&out, "debug")

	outerAccessMu.Lock()
	previous := outerAccessLogger
	outerAccessLogger = logger
	outerAccessMu.Unlock()
	t.Cleanup(func() {
		outerAccessMu.Lock()
		outerAccessLogger = previous
		outerAccessMu.Unlock()
	})

	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.RequestIDKey, "req-9"))
	outerAccessLogFormatter{}.NewLogEntry(req).Panic("boom", []byte("stack"))

	if !strings.Contains(out.String(), "req-9") {
		t.Errorf("the request id is missing: %s", out.String())
	}
}

// ─── The app and its assets ───────────────────────────────────────────────────

// The app mount is whatever the deployment configured, and nothing when it
// configured nothing. A mount that exists but points nowhere would answer 502
// for every path the services do not claim.
func TestTheAppMount(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		cfg     config.Config
		mounted bool
	}{
		"nothing configured":       {config.Config{Mode: "standalone"}, false},
		"a static directory":       {config.Config{Mode: "standalone", AppMode: "static", AppStaticDir: dir}, true},
		"static with no directory": {config.Config{Mode: "standalone", AppMode: "static"}, false},
		"a proxy":                  {config.Config{Mode: "standalone", AppMode: "proxy", AppUpstream: "http://app:3000"}, true},
		"a proxy with no upstream": {config.Config{Mode: "standalone", AppMode: "proxy"}, false},
		"a proxy to a bad URL":     {config.Config{Mode: "standalone", AppMode: "proxy", AppUpstream: "://nonsense"}, false},
		"a mode nobody recognises": {config.Config{Mode: "standalone", AppMode: "carrier-pigeon"}, false},
	} {
		cfg := tc.cfg
		d := depsFor(t, &cfg, &proxy.RouteManifest{Schema: "public"})
		if got := buildApp(d) != nil; got != tc.mounted {
			t.Errorf("%s: mounted = %v, want %v", name, got, tc.mounted)
		}
	}
}

// The Vite HMR proxy is mounted only when a dev server was named, and only when
// the name is a URL that can be reached.
func TestTheViteMount(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg     config.Config
		mounted bool
	}{
		"nothing configured":        {config.Config{Mode: "standalone"}, false},
		"a dev server":              {config.Config{Mode: "standalone", ViteDevURL: "http://localhost:5173"}, true},
		"a URL with no host":        {config.Config{Mode: "standalone", ViteDevURL: "not-a-url"}, false},
		"a URL that will not parse": {config.Config{Mode: "standalone", ViteDevURL: "://nonsense"}, false},
		// Outside proxy mode the app upstream doubles as the dev server, which is
		// what `supatype dev` configures.
		"an app upstream outside proxy mode": {
			config.Config{Mode: "standalone", AppUpstream: "http://localhost:5173"}, true,
		},
		"an app upstream in proxy mode": {
			config.Config{Mode: "standalone", AppMode: "proxy", AppUpstream: "http://localhost:5173"}, false,
		},
	} {
		cfg := tc.cfg
		d := depsFor(t, &cfg, &proxy.RouteManifest{Schema: "public"})
		if got := buildVite(d) != nil; got != tc.mounted {
			t.Errorf("%s: mounted = %v, want %v", name, got, tc.mounted)
		}
	}
}

// ─── Storage ──────────────────────────────────────────────────────────────────

// A storage mount with nowhere to send a request says so, rather than proxying
// to an empty address.
func TestStorageWithNoUpstream(t *testing.T) {
	for name, cfg := range map[string]config.Config{
		"nothing configured":    {Mode: "standalone"},
		"a URL that is not one": {Mode: "standalone", StorageURL: "://nonsense"},
	} {
		c := cfg
		d := depsFor(t, &c, &proxy.RouteManifest{Schema: "public"})

		rec := httptest.NewRecorder()
		buildStorage(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/object/public/b/x.png", nil))
		if rec.Code != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502", name, rec.Code)
		}
	}
}

// A local store is served from disk rather than proxied, which is what makes
// storage work out of the box with no MinIO.
func TestLocalStorageIsServedFromDisk(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Mode: "dev", StorageProvider: "local", StoragePath: dir, JWTSecret: "s"}
	d := depsFor(t, &cfg, &proxy.RouteManifest{Schema: "public"})

	rec := httptest.NewRecorder()
	buildStorage(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bucket", nil))
	// Unauthenticated, so it is refused rather than proxied — the point is that
	// the local handler answered at all.
	if rec.Code == http.StatusBadGateway {
		t.Errorf("the local store proxied instead of serving: %s", rec.Body.String())
	}
}

// ─── The row cap header ───────────────────────────────────────────────────────

// A configured row cap reaches PostgREST as a Prefer header, which is the only
// way it takes effect.
func TestTheRowCapReachesPostgREST(t *testing.T) {
	var seen http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := config.Config{
		Mode:          "standalone",
		PostgRESTURL:  upstream.URL,
		ApiConfigPath: filepath.Join(t.TempDir(), "api.json"),
	}
	d := depsFor(t, &cfg, &proxy.RouteManifest{Schema: "public"})

	api := apiconfig.DefaultApiConfig()
	api.Rest.MaxRows = 25
	if err := d.APIStore.Set(t.Context(), api); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	restProxyHandler(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/posts", nil))

	if got := seen.Get("Prefer"); got != "max-rows=25" {
		t.Errorf("Prefer = %q, want the configured cap", got)
	}
	if got := seen.Get("X-Pg-Schema"); got != "public" {
		t.Errorf("X-Pg-Schema = %q", got)
	}
}

// ─── The control plane proxy ──────────────────────────────────────────────────

// The platform proxy is mounted only when it can be built. It used to panic on
// an unparseable URL; refusing to mount is the answer, because the rest of the
// stack is still worth serving.
func TestThePlatformProxyIsNotMountedWhenItCannotBeBuilt(t *testing.T) {
	cfg := config.Config{Mode: "managed", ControlPlaneURL: "://nonsense"}
	d := depsFor(t, &cfg, &proxy.RouteManifest{Schema: "public"})

	if buildPlatform(d) != nil {
		t.Error("a proxy was mounted at an address that will not parse")
	}
}

// ─── Which worker serves a function ───────────────────────────────────────────

// The functions upstream is resolved per request, because a managed deployment
// picks the worker from the calling tenant's manifest. Sending a hook to the
// wrong worker is a 503 on every write to a hooked table.
func TestResolvingTheFunctionsUpstream(t *testing.T) {
	configured := &config.Config{FunctionsWorkerURL: "http://config-worker:9000", DenoPort: "9001"}
	bare := &config.Config{DenoPort: "9001"}

	for name, tc := range map[string]struct {
		cfg      *config.Config
		manifest *proxy.RouteManifest
		fnName   string
		deno     bool
		want     string
		wantErr  bool
	}{
		"the function's own worker wins": {
			configured,
			&proxy.RouteManifest{
				FunctionWorkerURLs: map[string]string{"send": "http://send-worker:8000"},
				FunctionsWorkerURL: "http://tenant-worker:8000",
			},
			"send", false, "http://send-worker:8000", false,
		},
		"then the tenant's worker": {
			configured,
			&proxy.RouteManifest{FunctionsWorkerURL: "http://tenant-worker:8000"},
			"send", false, "http://tenant-worker:8000", false,
		},
		"then the configured worker": {
			configured, &proxy.RouteManifest{}, "send", false, "http://config-worker:9000", false,
		},
		"and the local Deno process last": {
			bare, nil, "send", true, "http://127.0.0.1:9001", false,
		},
		"a named function with no manifest falls through": {
			configured, nil, "send", false, "http://config-worker:9000", false,
		},
		"nowhere to send it": {
			bare, nil, "send", false, "", true,
		},
	} {
		u, err := resolveFunctionsUpstreamURL(tc.cfg, tc.manifest, tc.fnName, tc.deno)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr = %v", name, err, tc.wantErr)
			continue
		}
		if err == nil && u.String() != tc.want {
			t.Errorf("%s: upstream = %s, want %s", name, u, tc.want)
		}
	}
}

// The configured worker has to be a whole address. A bare hostname parses as a
// path, so proxying to it would post the request back to the gateway itself.
func TestTheConfiguredWorkerMustBeAWholeAddress(t *testing.T) {
	for name, raw := range map[string]string{
		"no scheme or host":     "worker:8000",
		"a URL that is not one": "://nonsense",
	} {
		if _, err := functionsUpstreamURL(&config.Config{FunctionsWorkerURL: raw}); err == nil {
			t.Errorf("%s: %q was accepted", name, raw)
		}
	}
}

// A hook's own Deployment is registered under its namespaced name, so a project
// may have a hook and a public function sharing a name without one resolving to
// the other's pod.
func TestResolvingAHookUpstream(t *testing.T) {
	cfg := &config.Config{FunctionsWorkerURL: "http://config-worker:9000"}

	manifest := &proxy.RouteManifest{
		FunctionWorkerURLs: map[string]string{
			"hooks/audit": "http://hook-pod:8000/",
			"audit":       "http://public-pod:8000",
		},
	}
	got, err := hookUpstreamURL(cfg, manifest, "audit", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://hook-pod:8000/hooks/audit" {
		t.Errorf("upstream = %s, want the hook's own pod", got)
	}

	// With no entry of its own the hook goes to the project's worker, under the
	// same hooks/ namespace.
	got, err = hookUpstreamURL(cfg, &proxy.RouteManifest{}, "audit", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://config-worker:9000/hooks/audit" {
		t.Errorf("upstream = %s", got)
	}

	// And with no worker anywhere it is an error, not an empty address.
	if _, err := hookUpstreamURL(&config.Config{}, nil, "audit", false); err == nil {
		t.Error("want an error when there is no worker")
	}
}

// The realtime upstream follows the same precedence, one level shorter.
func TestResolvingTheRealtimeUpstream(t *testing.T) {
	configured := &config.Config{RealtimeURL: "http://config-realtime:4000"}

	for name, tc := range map[string]struct {
		cfg      *config.Config
		manifest *proxy.RouteManifest
		want     string
		wantErr  bool
	}{
		"the tenant's own": {configured, &proxy.RouteManifest{RealtimeURL: "http://tenant:4000"}, "http://tenant:4000", false},
		"then the config":  {configured, &proxy.RouteManifest{}, "http://config-realtime:4000", false},
		"and nothing else": {&config.Config{}, nil, "", true},
	} {
		u, err := resolveRealtimeUpstreamURL(tc.cfg, tc.manifest)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		if err == nil && u.String() != tc.want {
			t.Errorf("%s: upstream = %s, want %s", name, u, tc.want)
		}
	}
}

func TestTheFirstURLSegment(t *testing.T) {
	for path, want := range map[string]string{
		"/send/mail": "send",
		"send/mail":  "send",
		"/send":      "send",
		"/":          "",
		"":           "",
	} {
		if got := firstURLSegment(path); got != want {
			t.Errorf("firstURLSegment(%q) = %q, want %q", path, got, want)
		}
	}
}

// ─── Invoking a function ──────────────────────────────────────────────────────

// What the invocation proxy does before it forwards anything: hooks are not a
// public endpoint, a tenant that has not enabled functions has none, and an
// upstream that will not resolve is a 502 rather than a panic.
func TestTheFunctionsInvocationProxy(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("the worker answered"))
	}))
	defer worker.Close()

	served := &config.Config{FunctionsWorkerURL: worker.URL}

	for name, tc := range map[string]struct {
		cfg      *config.Config
		manifest *proxy.RouteManifest
		path     string
		want     int
	}{
		"a function is forwarded": {
			served, &proxy.RouteManifest{FunctionsEnabled: true}, "/send", http.StatusOK,
		},
		"a hook is not invocable from outside": {
			served, &proxy.RouteManifest{FunctionsEnabled: true}, "/hooks/audit", http.StatusNotFound,
		},
		"functions off and none configured": {
			&config.Config{}, &proxy.RouteManifest{FunctionsEnabled: false}, "/send", http.StatusNotFound,
		},
		"functions off but one configured is still served": {
			served, &proxy.RouteManifest{FunctionsEnabled: false}, "/send", http.StatusOK,
		},
		"no upstream to resolve": {
			&config.Config{}, nil, "/send", http.StatusBadGateway,
		},
	} {
		manifest := tc.manifest
		h := functionsInvocationProxy(tc.cfg, func(*http.Request) *proxy.RouteManifest { return manifest }, false)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// Realtime is off unless the tenant turned it on, and a tenant that turned it
// on with nowhere to send it gets a 502.
func TestTheRealtimeInvocationProxy(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer service.Close()

	for name, tc := range map[string]struct {
		cfg      *config.Config
		manifest *proxy.RouteManifest
		want     int
	}{
		"enabled and configured":   {&config.Config{RealtimeURL: service.URL}, &proxy.RouteManifest{RealtimeEnabled: true}, http.StatusOK},
		"not enabled":              {&config.Config{RealtimeURL: service.URL}, &proxy.RouteManifest{}, http.StatusNotFound},
		"enabled with no upstream": {&config.Config{}, &proxy.RouteManifest{RealtimeEnabled: true}, http.StatusBadGateway},
		"no manifest at all":       {&config.Config{RealtimeURL: service.URL}, nil, http.StatusOK},
	} {
		manifest := tc.manifest
		h := realtimeInvocationProxy(tc.cfg, func(*http.Request) *proxy.RouteManifest { return manifest })

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/websocket", nil))
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, tc.want)
		}
	}
}

// ─── Cache headers for the app's assets ───────────────────────────────────────

// A tenant's manifest overrides the deployment's cache policy field by field,
// so a project can pin its own without restating the rest.
func TestTheStaticCachePolicy(t *testing.T) {
	cfg := &config.Config{
		StaticCacheHTML:         "no-store",
		StaticCacheHashedAssets: "max-age=31536000",
		StaticCachePrefixesJSON: `{"/img/":"max-age=60"}`,
	}

	base := staticCacheOpts(cfg, nil)
	if base.HTML != "no-store" || base.HashedAssets != "max-age=31536000" {
		t.Errorf("the configured policy did not survive: %+v", base)
	}
	if base.Prefixes["/img/"] != "max-age=60" {
		t.Errorf("prefixes = %v", base.Prefixes)
	}

	overridden := staticCacheOpts(cfg, &proxy.RouteManifest{
		StaticCacheHTML:         "max-age=5",
		StaticCacheHashedAssets: "max-age=600",
		StaticCachePrefixes:     map[string]string{"/fonts/": "max-age=99"},
	})
	if overridden.HTML != "max-age=5" || overridden.HashedAssets != "max-age=600" {
		t.Errorf("the manifest did not override: %+v", overridden)
	}
	// The manifest's prefixes are merged onto the configured ones rather than
	// replacing them.
	if overridden.Prefixes["/fonts/"] != "max-age=99" || overridden.Prefixes["/img/"] != "max-age=60" {
		t.Errorf("prefixes = %v", overridden.Prefixes)
	}

	// A manifest with prefixes and no configured ones starts a map rather than
	// writing into a nil one.
	fresh := staticCacheOpts(&config.Config{}, &proxy.RouteManifest{
		StaticCachePrefixes: map[string]string{"/img/": "max-age=1"},
	})
	if fresh.Prefixes["/img/"] != "max-age=1" {
		t.Errorf("prefixes = %v", fresh.Prefixes)
	}
}

// Cache policy that will not parse is ignored rather than fatal: the assets are
// still worth serving under the default policy.
func TestUnparseableCachePrefixesAreIgnored(t *testing.T) {
	if got := parseStaticPrefixesJSON("  "); got != nil {
		t.Errorf("blank = %v, want nil", got)
	}
	if got := parseStaticPrefixesJSON("{not json"); got != nil {
		t.Errorf("nonsense = %v, want nil", got)
	}
	if got := parseStaticPrefixesJSON(`{"/a/":"max-age=1"}`); got["/a/"] != "max-age=1" {
		t.Errorf("parsed = %v", got)
	}
}

// A configured worker that is a whole address is used as given.
func TestTheConfiguredWorkerIsUsedAsGiven(t *testing.T) {
	u, err := functionsUpstreamURL(&config.Config{FunctionsWorkerURL: "http://worker:8000"})
	if err != nil {
		t.Fatal(err)
	}
	if u.String() != "http://worker:8000" {
		t.Errorf("upstream = %s", u)
	}
}

// ─── Hooks with nothing configured ────────────────────────────────────────────

// A deployment with no manifest has no hooks and no validators, so a write goes
// straight through. Reading them off a nil manifest would panic on the first
// write instead.
func TestHookedWritesWithNoManifest(t *testing.T) {
	cfg := &config.Config{Mode: "standalone", JWTSecret: "secret"}
	resources, err := data.Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resources.Close() })

	d := NewDeps(
		cfg,
		func(*http.Request) *proxy.RouteManifest { return nil },
		nil,
		http.NotFoundHandler(),
		nil,
		"test",
		resources,
		http.NotFoundHandler(),
	)

	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusCreated)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(`{"title":"x"}`))
	newHookMiddleware(d)(next).ServeHTTP(rec, req)

	if !reached {
		t.Errorf("the write did not go through: status = %d (%s)", rec.Code, rec.Body.String())
	}
}

// ─── The access log ───────────────────────────────────────────────────────────

// What a log line says about the request it describes. The tenant is the field
// that makes a managed deployment's log readable at all, and the mode is what
// says which of the four shapes served it.
func TestWhatALogLineSaysAboutARequest(t *testing.T) {
	fields := logrus.Fields{}
	WithOuterAccessLogContext("managed", "")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		describeRequest(fields, r)
	})).ServeHTTP(httptest.NewRecorder(), func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts?select=id&limit=1", nil)
		req.Header.Set("X-Supatype-Tenant", "proj-abc")
		return req
	}())

	for key, want := range map[string]string{
		"mode":   "managed",
		"query":  "select=id&limit=1",
		"tenant": "proj-abc",
	} {
		if got, _ := fields[key].(string); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	// And a request with none of them says none of them, rather than three empty
	// columns on every line of a standalone deployment's log.
	bare := logrus.Fields{}
	describeRequest(bare, httptest.NewRequest(http.MethodGet, "/health", nil))
	if len(bare) != 0 {
		t.Errorf("fields = %v, want none", bare)
	}
}

// The previous() callback reads the rows a write is about to change, and it
// reads them from wherever this request's REST traffic goes. Resolving that
// once at startup instead would send a managed tenant's hook to another
// tenant's database.
func TestThePreviousCallbackReadsFromTheTenantsPostgREST(t *testing.T) {
	var seen string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Accept-Profile")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1}]`))
	}))
	defer rest.Close()

	cfg := &config.Config{Mode: "standalone", ServiceRoleKey: "service-role"}
	d := depsFor(t, cfg, &proxy.RouteManifest{Schema: "app", PostgRESTURL: rest.URL})

	callback := newHookCallback(d)
	if callback == nil {
		t.Fatal("no callback")
	}
	token := strings.TrimPrefix(
		callback.Path(modelhooks.OpUpdate, "posts", "id=eq.1"), modelhooks.PreviousPathPrefix)

	rec := httptest.NewRecorder()
	callback.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/"+token, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if seen != "app" {
		t.Errorf("the fetch asked for schema %q, want the tenant's", seen)
	}
}
