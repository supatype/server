package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
)

// switchMux builds the outer mux over one upstream that answers every service, on a manifest the
// test replaces between requests, as the live reload does.
func switchMux(t *testing.T, cfg func(upstream string) *config.Config) (http.Handler, func(string), *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	c := cfg(upstream.URL)
	var live atomic.Pointer[proxy.RouteManifest]
	set := func(manifestJSON string) {
		t.Helper()
		m, err := proxy.ParseRouteManifestJSON([]byte(strings.ReplaceAll(manifestJSON, "UPSTREAM", upstream.URL)))
		if err != nil {
			t.Fatalf("manifest %s: %v", manifestJSON, err)
		}
		live.Store(m)
	}
	set(`{}`)
	mf := func(*http.Request) *proxy.RouteManifest { return live.Load() }
	hp := func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(c, live.Load(), "") }
	return buildOuterMux(c, mf, hp, http.NotFoundHandler(), nil, "test", nil, nil), set, &hits
}

func statusOf(handler http.Handler, path string) int {
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, strings.NewReader("")))
	return rr.Code
}

// 0.9.2: every switchable service is switched by one shape, the manifest's
// services.<name>.enabled, read per request over the server's own start value.
// Realtime had a bool of its own, storage an inverted one, functions a third.
func TestEveryServiceIsSwitchedByTheServicesMap(t *testing.T) {
	handler, set, _ := switchMux(t, func(up string) *config.Config {
		return &config.Config{Mode: "dev", PostgRESTURL: up, RealtimeURL: up, StorageDisabled: true, DenoFunctionsDir: t.TempDir()}
	})
	for _, tc := range []struct {
		name, manifest, path string
		want                 int
	}{
		{"storage started off, switched on", `{"storage_url":"UPSTREAM","services":{"storage":{"enabled":true}}}`, "/storage/v1/bucket", http.StatusOK},
		{"storage switched off again", `{"storage_url":"UPSTREAM","services":{"storage":{"enabled":false}}}`, "/storage/v1/bucket", http.StatusServiceUnavailable},
		{"realtime switched on", `{"services":{"realtime":{"enabled":true}}}`, "/realtime/v1/health", http.StatusOK},
		{"realtime switched off", `{"services":{"realtime":{"enabled":false}}}`, "/realtime/v1/health", http.StatusNotFound},
		{"functions switched on", `{"functions_worker_url":"UPSTREAM","services":{"functions":{"enabled":true}}}`, "/functions/v1/send", http.StatusOK},
		{"the services map wins over the legacy key", `{"realtime_enabled":true,"services":{"realtime":{"enabled":false}}}`, "/realtime/v1/health", http.StatusNotFound},
	} {
		set(tc.manifest)
		if got := statusOf(handler, tc.path); got != tc.want {
			t.Errorf("%s: %s answered %d, want %d", tc.name, tc.path, got, tc.want)
		}
	}
}

// Cloud and older CLIs write realtime_enabled and functions_enabled. They are still read, as the
// same switch.
func TestTheLegacySwitchesAreStillRead(t *testing.T) {
	handler, set, _ := switchMux(t, func(up string) *config.Config {
		return &config.Config{Mode: "dev", PostgRESTURL: up, RealtimeURL: up, DenoFunctionsDir: t.TempDir()}
	})
	set(`{"realtime_enabled":true,"functions_enabled":true,"functions_worker_url":"UPSTREAM"}`)
	if got := statusOf(handler, "/realtime/v1/health"); got != http.StatusOK {
		t.Errorf("realtime_enabled true: %d, want 200", got)
	}
	if got := statusOf(handler, "/functions/v1/send"); got != http.StatusOK {
		t.Errorf("functions_enabled true: %d, want 200", got)
	}
	set(`{"realtime_enabled":false}`)
	if got := statusOf(handler, "/realtime/v1/health"); got != http.StatusNotFound {
		t.Errorf("realtime_enabled false: %d, want 404", got)
	}
}

// A manifest that says nothing about a service leaves the server's start value: storage on unless
// SUPATYPE_STORAGE_DISABLED, functions on when a worker is configured, realtime off.
func TestAServiceTheManifestSaysNothingAboutKeepsItsStartValue(t *testing.T) {
	handler, set, _ := switchMux(t, func(up string) *config.Config {
		return &config.Config{Mode: "dev", PostgRESTURL: up, RealtimeURL: up, StorageURL: up, FunctionsWorkerURL: up}
	})
	set(`{}`)
	for path, want := range map[string]int{
		"/storage/v1/bucket":  http.StatusOK,
		"/functions/v1/send":  http.StatusOK,
		"/realtime/v1/health": http.StatusNotFound,
	} {
		if got := statusOf(handler, path); got != want {
			t.Errorf("%s answered %d, want %d", path, got, want)
		}
	}
}
