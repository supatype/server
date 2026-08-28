package outerhealth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// What readiness is for is telling an orchestrator not to send traffic yet, so
// the cases that matter are the ones where a component is missing, misconfigured
// or answering badly. Those were the untested ones.

const probeTimeout = 500 * time.Millisecond

// answering returns a server that replies with this status to everything.
func answering(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}

// component reads one component out of a collected report.
func component(t *testing.T, components map[string]any, name string) map[string]any {
	t.Helper()
	value, ok := components[name].(map[string]any)
	if !ok {
		t.Fatalf("no %s component in %#v", name, components)
	}
	return value
}

// ─── Probing ──────────────────────────────────────────────────────────────────

// A 401 or a 404 means the upstream is up and answering; only a 5xx or no
// answer at all means it is not. Treating an auth challenge as down would make
// a correctly secured upstream look broken.
func TestProbeHTTPGetAcceptsAnyNon5xx(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		want   bool
	}{
		"200 OK":                        {http.StatusOK, true},
		"401 Unauthorized":              {http.StatusUnauthorized, true},
		"404 Not Found":                 {http.StatusNotFound, true},
		"499, still not a server error": {499, true},
		"500 Internal Server Error":     {http.StatusInternalServerError, false},
		"503 Service Unavailable":       {http.StatusServiceUnavailable, false},
	} {
		if got := probeHTTPGet(answering(t, tc.status).URL, probeTimeout); got != tc.want {
			t.Errorf("%s: ready = %v, want %v", name, got, tc.want)
		}
	}
}

func TestProbeHTTPGetOnAnythingItCannotReach(t *testing.T) {
	for name, url := range map[string]string{
		"no URL":            "",
		"only whitespace":   "   ",
		"not a URL at all":  "://nonsense",
		"a control charact": "http://exa\x7fmple.com/",
		"a closed port":     "http://127.0.0.1:9/",
	} {
		if probeHTTPGet(url, probeTimeout) {
			t.Errorf("%s: reported ready", name)
		}
	}
}

func TestJoinURL(t *testing.T) {
	for name, tc := range map[string]struct {
		base, path, want string
	}{
		"a base and a path":         {"http://h:1", "/x", "http://h:1/x"},
		"a trailing slash on base":  {"http://h:1/", "/x", "http://h:1/x"},
		"several trailing slashes":  {"http://h:1///", "/x", "http://h:1/x"},
		"a path without its slash":  {"http://h:1", "x", "http://h:1/x"},
		"the root path":             {"http://h:1", "/", "http://h:1/"},
		"no path":                   {"http://h:1", "", "http://h:1/"},
		"a path of only whitespace": {"http://h:1", "  ", "http://h:1/"},
		"padding around the base":   {"  http://h:1  ", "/x", "http://h:1/x"},
		"no base":                   {"", "/x", ""},
		"a base of only whitespace": {"   ", "/x", ""},
		"a base of only slashes":    {"///", "/x", ""},
	} {
		if got := joinURL(tc.base, tc.path); got != tc.want {
			t.Errorf("%s: joinURL(%q, %q) = %q, want %q", name, tc.base, tc.path, got, tc.want)
		}
	}
}

// ─── Storage ──────────────────────────────────────────────────────────────────

// Local storage is ready when the directory is there and is a directory. A file
// at the path is a misconfiguration that would otherwise fail on the first
// upload.
func TestLocalStorageReadiness(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		path string
		want bool
	}{
		"a directory that exists": {dir, true},
		"a file, not a directory": {file, false},
		"nothing at that path":    {filepath.Join(dir, "absent"), false},
	} {
		components := collectComponents(ProbeConfig{StorageLocalPath: tc.path}, probeTimeout)
		storage := component(t, components, "storage")

		if storage["mode"] != "local" {
			t.Errorf("%s: mode = %v, want local", name, storage["mode"])
		}
		if storage["ready"] != tc.want {
			t.Errorf("%s: ready = %v, want %v", name, storage["ready"], tc.want)
		}
	}
}

func TestRemoteStorageIsProbed(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		want   bool
	}{
		"the store answers":   {http.StatusOK, true},
		"the store is unwell": {http.StatusInternalServerError, false},
	} {
		url := answering(t, tc.status).URL
		components := collectComponents(ProbeConfig{StorageRemoteURL: url}, probeTimeout)
		storage := component(t, components, "storage")

		if storage["mode"] != "remote" {
			t.Errorf("%s: mode = %v, want remote", name, storage["mode"])
		}
		if storage["ready"] != tc.want {
			t.Errorf("%s: ready = %v, want %v", name, storage["ready"], tc.want)
		}
	}
}

// A deployment with no storage at all is ready, not degraded: storage is
// optional and a skipped component must not hold the whole service down.
func TestNoStorageConfiguredIsSkippedAndReady(t *testing.T) {
	storage := component(t, collectComponents(ProbeConfig{}, probeTimeout), "storage")

	if storage["skipped"] != true || storage["ready"] != true {
		t.Errorf("storage = %#v, want skipped and ready", storage)
	}
}

// Local wins when both are set, because that is what the request path uses.
func TestLocalStorageTakesPrecedenceOverRemote(t *testing.T) {
	components := collectComponents(ProbeConfig{
		StorageLocalPath: t.TempDir(),
		StorageRemoteURL: answering(t, http.StatusInternalServerError).URL,
	}, probeTimeout)

	if got := component(t, components, "storage")["mode"]; got != "local" {
		t.Errorf("mode = %v, want local", got)
	}
}

// ─── The other components ─────────────────────────────────────────────────────

// PostgREST with no URL is a misconfiguration, not an absence: the data plane
// cannot serve without it, so it reports not ready rather than skipped.
func TestPostgRESTWithNoURLIsNotReady(t *testing.T) {
	postgrest := component(t, collectComponents(ProbeConfig{}, probeTimeout), "postgrest")

	if postgrest["ready"] != false {
		t.Errorf("postgrest = %#v, want not ready", postgrest)
	}
	if _, skipped := postgrest["skipped"]; skipped {
		t.Error("a missing PostgREST URL is a misconfiguration, not something to skip")
	}
}

// GraphQL is served by PostgREST unless it has its own address.
func TestGraphQLFallsBackToPostgREST(t *testing.T) {
	postgrest := answering(t, http.StatusOK)
	graphql := answering(t, http.StatusOK)

	for name, tc := range map[string]struct {
		probes ProbeConfig
		want   string
	}{
		"its own address": {
			ProbeConfig{PostgRESTURL: postgrest.URL, GraphQLURL: graphql.URL},
			graphql.URL + "/graphql/v1",
		},
		"PostgREST's": {
			ProbeConfig{PostgRESTURL: postgrest.URL},
			postgrest.URL + "/graphql/v1",
		},
	} {
		got := component(t, collectComponents(tc.probes, probeTimeout), "graphql")
		if got["url"] != tc.want {
			t.Errorf("%s: url = %v, want %v", name, got["url"], tc.want)
		}
		if got["ready"] != true {
			t.Errorf("%s: ready = %v", name, got["ready"])
		}
	}
}

func TestGraphQLWithNowhereToLookIsNotReady(t *testing.T) {
	graphql := component(t, collectComponents(ProbeConfig{}, probeTimeout), "graphql")
	if graphql["ready"] != false {
		t.Errorf("graphql = %#v", graphql)
	}
}

func TestDenoIsSkippedWhenUnconfiguredAndProbedWhenNot(t *testing.T) {
	skipped := component(t, collectComponents(ProbeConfig{}, probeTimeout), "deno")
	if skipped["skipped"] != true || skipped["ready"] != true {
		t.Errorf("deno = %#v, want skipped and ready", skipped)
	}

	probed := component(t, collectComponents(
		ProbeConfig{DenoBaseURL: answering(t, http.StatusOK).URL}, probeTimeout), "deno")
	if probed["ready"] != true {
		t.Errorf("deno = %#v, want ready", probed)
	}

	unwell := component(t, collectComponents(
		ProbeConfig{DenoBaseURL: answering(t, http.StatusBadGateway).URL}, probeTimeout), "deno")
	if unwell["ready"] != false {
		t.Errorf("deno = %#v, want not ready", unwell)
	}
}

// ─── Aggregation ──────────────────────────────────────────────────────────────

// Readiness is a conjunction over the components that were actually checked. A
// skipped component is not a failure, except realtime, which is skipped only
// when it is enabled and cannot be reached.
func TestAggregateReady(t *testing.T) {
	ready := map[string]any{"ready": true}
	notReady := map[string]any{"ready": false}
	skipped := map[string]any{"skipped": true, "ready": true}

	for name, tc := range map[string]struct {
		components map[string]any
		want       bool
	}{
		"everything ready": {
			map[string]any{"postgrest": ready, "graphql": ready, "storage": ready, "deno": ready, "realtime": ready},
			true,
		},
		"one component down": {
			map[string]any{"postgrest": notReady, "graphql": ready},
			false,
		},
		"a skipped component does not count against it": {
			map[string]any{"postgrest": ready, "storage": skipped},
			true,
		},
		"realtime enabled but unreachable": {
			map[string]any{"postgrest": ready, "realtime": map[string]any{"skipped": true, "enabled": true, "ready": false}},
			false,
		},
		"realtime disabled and skipped": {
			map[string]any{"postgrest": ready, "realtime": map[string]any{"skipped": true, "enabled": false, "ready": true}},
			true,
		},
		"realtime skipped with no enabled flag at all": {
			map[string]any{"postgrest": ready, "realtime": map[string]any{"skipped": true}},
			true,
		},
		"a component nobody reported": {
			map[string]any{"postgrest": ready},
			true,
		},
		"a component that is not a report": {
			map[string]any{"postgrest": ready, "storage": "not a map"},
			true,
		},
		"a ready field that is not a boolean": {
			map[string]any{"postgrest": map[string]any{"ready": "yes"}},
			false,
		},
		"a skipped field that is not a boolean": {
			map[string]any{"postgrest": map[string]any{"skipped": "yes", "ready": true}},
			true,
		},
		"nothing at all": {map[string]any{}, true},
	} {
		if got := aggregateReady(tc.components); got != tc.want {
			t.Errorf("%s: ready = %v, want %v", name, got, tc.want)
		}
		wantStatus := "degraded"
		if tc.want {
			wantStatus = "ok"
		}
		if got := overallStatus(tc.components); got != wantStatus {
			t.Errorf("%s: status = %q, want %q", name, got, wantStatus)
		}
	}
}

// ─── The endpoints ────────────────────────────────────────────────────────────

// /health always answers 200 and describes itself; /health/ready answers 503
// when it is not ready, because that is the answer an orchestrator acts on.
func TestReadyReportsUnreadinessInItsStatusCode(t *testing.T) {
	router := chi.NewRouter()
	Attach(router, &config.Config{Mode: "dev"}, "9.9.9", func() ProbeConfig {
		return ProbeConfig{} // no PostgREST, so not ready
	})

	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK {
		t.Errorf("/health status = %d, want 200 whatever the components say", health.Code)
	}

	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Errorf("/health/ready status = %d, want 503", ready.Code)
	}

	var body struct {
		Ready     bool   `json:"ready"`
		Status    string `json:"status"`
		CheckedAt string `json:"checked_at"`
	}
	if err := json.Unmarshal(ready.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || body.Status != "degraded" || body.CheckedAt == "" {
		t.Errorf("body = %+v", body)
	}
}

// The probes are called per scrape, not once at mount, so a manifest that
// changes is reflected without a restart.
func TestTheProbesAreCalledOnEveryScrape(t *testing.T) {
	var calls int
	router := chi.NewRouter()
	Attach(router, &config.Config{}, "v", func() ProbeConfig {
		calls++
		return ProbeConfig{}
	})

	for i := 0; i < 3; i++ {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	}
	if calls != 6 {
		t.Errorf("probes called %d times, want one per scrape", calls)
	}
}

// ─── Probe configuration ──────────────────────────────────────────────────────

// The manifest overrides configuration, configuration overrides the default.
// Getting this order wrong sends a tenant's health check at another tenant's
// upstream.
func TestProbeConfigFromResolutionOrder(t *testing.T) {
	cfg := &config.Config{PostgRESTURL: "http://cfg-rest", GraphQLURL: "http://cfg-gql", StorageURL: "http://cfg-store"}
	manifest := &proxy.RouteManifest{PostgRESTURL: "http://m-rest", GraphQLURL: "http://m-gql", StorageURL: "http://m-store"}

	fromManifest := ProbeConfigFrom(cfg, manifest, " http://deno ")
	if fromManifest.PostgRESTURL != "http://m-rest" || fromManifest.GraphQLURL != "http://m-gql" ||
		fromManifest.StorageRemoteURL != "http://m-store" {
		t.Errorf("the manifest did not win: %+v", fromManifest)
	}
	if fromManifest.DenoBaseURL != "http://deno" {
		t.Errorf("deno base = %q, want it trimmed", fromManifest.DenoBaseURL)
	}

	fromConfig := ProbeConfigFrom(cfg, &proxy.RouteManifest{}, "")
	if fromConfig.PostgRESTURL != "http://cfg-rest" || fromConfig.GraphQLURL != "http://cfg-gql" {
		t.Errorf("configuration did not win over the default: %+v", fromConfig)
	}

	fromDefault := ProbeConfigFrom(&config.Config{}, nil, "")
	if fromDefault.PostgRESTURL != "http://localhost:3000" {
		t.Errorf("postgrest = %q, want the built-in default", fromDefault.PostgRESTURL)
	}
	// With no GraphQL address anywhere, PostgREST serves it.
	if fromDefault.GraphQLURL != "http://localhost:3000" {
		t.Errorf("graphql = %q, want PostgREST's address", fromDefault.GraphQLURL)
	}
}

// Local storage is only local when it is configured as such and given a path.
// A provider of "local" with no path is not a local store.
func TestProbeConfigFromStorageMode(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg        config.Config
		manifest   proxy.RouteManifest
		wantLocal  string
		wantRemote string
	}{
		"local with a path": {
			config.Config{StorageProvider: "local", StoragePath: "/srv/files"}, proxy.RouteManifest{},
			"/srv/files", "",
		},
		"local with no path": {
			config.Config{StorageProvider: "local", StorageURL: "http://cfg"}, proxy.RouteManifest{},
			"", "http://cfg",
		},
		"a remote provider": {
			config.Config{StorageProvider: "s3", StoragePath: "/ignored", StorageURL: "http://cfg"}, proxy.RouteManifest{},
			"", "http://cfg",
		},
		"the manifest's store": {
			config.Config{StorageURL: "http://cfg"}, proxy.RouteManifest{StorageURL: "http://m"},
			"", "http://m",
		},
		"no store anywhere": {config.Config{}, proxy.RouteManifest{}, "", ""},
	} {
		cfg, manifest := tc.cfg, tc.manifest
		got := ProbeConfigFrom(&cfg, &manifest, "")
		if got.StorageLocalPath != tc.wantLocal || got.StorageRemoteURL != tc.wantRemote {
			t.Errorf("%s: local = %q, remote = %q, want %q and %q",
				name, got.StorageLocalPath, got.StorageRemoteURL, tc.wantLocal, tc.wantRemote)
		}
	}
}

func TestProbeConfigFromCarriesRealtime(t *testing.T) {
	got := ProbeConfigFrom(&config.Config{}, &proxy.RouteManifest{RealtimeEnabled: true}, "")
	if !got.RealtimeEnabled {
		t.Error("the manifest's realtime flag was dropped")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	for name, tc := range map[string]struct {
		values []string
		want   string
	}{
		"the first":               {[]string{"a", "b"}, "a"},
		"past a blank":            {[]string{"", "b"}, "b"},
		"past whitespace":         {[]string{"   ", "b"}, "b"},
		"nothing but blanks":      {[]string{"", "  "}, ""},
		"no values at all":        {nil, ""},
		"padding is not stripped": {[]string{" a "}, " a "},
	} {
		if got := firstNonEmpty(tc.values...); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
