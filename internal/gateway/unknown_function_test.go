package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// Free-tier projects run one pod per function and register each in the manifest's per-function map,
// with no shared worker. A name outside that map is a caller's typo, not a broken gateway, so it
// answers 404 and not 502.
func perFunctionManifest(urls map[string]string) *proxy.RouteManifest {
	return &proxy.RouteManifest{
		Services:           proxy.SwitchesOf(map[string]bool{proxy.ServiceFunctions: true}),
		FunctionWorkerURLs: urls,
	}
}

func invokeFunction(t *testing.T, cfg *config.Config, m *proxy.RouteManifest, path string) *httptest.ResponseRecorder {
	t.Helper()
	h := functionsInvocationProxy(cfg, func(*http.Request) *proxy.RouteManifest { return m }, false)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestAnUnknownFunctionOnAPerFunctionTenantIsNotFound(t *testing.T) {
	m := perFunctionManifest(map[string]string{"send": "http://fn-send.p.svc:8001"})

	rec := invokeFunction(t, &config.Config{}, m, "/nope")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	if body["error"] != "function not found" {
		t.Fatalf("body = %v, want error \"function not found\"", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestAKnownFunctionWhosePodIsDownIsStillABadGateway(t *testing.T) {
	// A closed port: the function is registered, so the failure is the gateway's, not the caller's.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	m := perFunctionManifest(map[string]string{"send": url})

	rec := invokeFunction(t, &config.Config{}, m, "/send")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (%s)", rec.Code, rec.Body.String())
	}
}

func TestAnUnknownNameWithASharedWorkerIsLeftToTheWorker(t *testing.T) {
	// Self-host and project-worker tenants: the worker answers unknown names itself.
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer worker.Close()

	cfgWorker := invokeFunction(t, &config.Config{FunctionsWorkerURL: worker.URL}, perFunctionManifest(map[string]string{"send": "http://x:1"}), "/nope")
	if cfgWorker.Code != http.StatusTeapot {
		t.Fatalf("env worker: status = %d, want it forwarded", cfgWorker.Code)
	}

	m := perFunctionManifest(map[string]string{"send": "http://x:1"})
	m.FunctionsWorkerURL = worker.URL
	manifestWorker := invokeFunction(t, &config.Config{}, m, "/nope")
	if manifestWorker.Code != http.StatusTeapot {
		t.Fatalf("manifest worker: status = %d, want it forwarded", manifestWorker.Code)
	}
}
