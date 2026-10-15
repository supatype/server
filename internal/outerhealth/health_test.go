package outerhealth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/config"
)

func TestAttachHealthJSON(t *testing.T) {
	r := chi.NewRouter()
	cfg := &config.Config{Mode: "dev"}
	Attach(r, cfg, "test-version", func() ProbeConfig { return ProbeConfig{PostgRESTURL: ""} })

	t.Run("health", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d", rr.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if body["status"] != "degraded" {
			t.Fatalf("expected degraded when postgrest unavailable, got %#v", body["status"])
		}
	})

	t.Run("ready_without_postgrest", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 when postgrest URL empty, got %d", rr.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if body["status"] != "degraded" {
			t.Fatalf("expected degraded ready status, got %#v", body["status"])
		}
	})
}

func TestAttachHealth_allProbesGreen(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/graphql/v1":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	r := chi.NewRouter()
	cfg := &config.Config{Mode: "dev"}
	Attach(r, cfg, "v1", func() ProbeConfig {
		return ProbeConfig{
			PostgRESTURL:     ts.URL,
			GraphQLURL:       ts.URL,
			StorageRemoteURL: ts.URL,
		}
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ready"] != true {
		t.Fatalf("expected ready true, got %#v", body["ready"])
	}
}

func TestCollectComponents_realtimeHTTPProbe(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/graphql/v1", "/realtime/v1/health":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	c := collectComponents(ProbeConfig{
		PostgRESTURL:    ts.URL,
		GraphQLURL:      ts.URL,
		RealtimeEnabled: true,
		SelfBaseURL:     ts.URL,
	}, time.Second)
	rt, ok := c["realtime"].(map[string]any)
	if !ok {
		t.Fatalf("realtime component missing: %#v", c)
	}
	if rt["ready"] != true {
		t.Fatalf("expected realtime ready, got %#v", rt)
	}
}

func TestCollectComponents_realtimeEnabledNoSelfBaseURLBlocksReady(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/graphql/v1":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	c := collectComponents(ProbeConfig{
		PostgRESTURL:     ts.URL,
		GraphQLURL:       ts.URL,
		StorageRemoteURL: ts.URL,
		RealtimeEnabled:  true,
		SelfBaseURL:      "",
	}, time.Second)
	if aggregateReady(c) {
		t.Fatalf("expected aggregate not ready, components=%#v", c)
	}
	rt, ok := c["realtime"].(map[string]any)
	if !ok || rt["ready"] != false {
		t.Fatalf("expected realtime ready=false, got %#v", rt)
	}
}

func TestProbePostgREST(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	if !probeHTTPGet(joinURL(ts.URL, "/"), time.Second) {
		t.Fatal("expected probe success")
	}
	if probeHTTPGet(joinURL("http://127.0.0.1:9", "/"), 100*time.Millisecond) {
		t.Fatal("expected probe failure on closed port")
	}
}

// graphqlDownUpstream answers 200 everywhere except /graphql/v1, which fails
// with 500 and is counted, so a test can prove the GraphQL probe never ran.
func graphqlDownUpstream(t *testing.T, graphqlHits *atomic.Int32) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql/v1" {
			graphqlHits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestHealthReady_graphqlDisabledIsReportedNotProbed(t *testing.T) {
	var hits atomic.Int32
	ts := graphqlDownUpstream(t, &hits)
	cfg := &config.Config{Mode: "dev", PostgRESTURL: ts.URL, StorageURL: ts.URL, GraphQLDisabled: true}
	r := chi.NewRouter()
	Attach(r, cfg, "v1", func() ProbeConfig { return ProbeConfigFrom(cfg, nil, "") })

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with graphql disabled, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Components map[string]map[string]any `json:"components"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	gq := body.Components["graphql"]
	if gq["status"] != "disabled" || gq["enabled"] != false {
		t.Fatalf("expected graphql status disabled, enabled false, got %#v", gq)
	}
	if _, has := gq["ready"]; has {
		t.Fatalf("disabled graphql must not claim a ready value, got %#v", gq)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("expected no GraphQL probe when disabled, got %d request(s)", n)
	}
}

func TestHealthReady_graphqlEnabledStillProbed(t *testing.T) {
	var hits atomic.Int32
	ts := graphqlDownUpstream(t, &hits)
	cfg := &config.Config{Mode: "dev", PostgRESTURL: ts.URL, StorageURL: ts.URL}
	r := chi.NewRouter()
	Attach(r, cfg, "v1", func() ProbeConfig { return ProbeConfigFrom(cfg, nil, "") })

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when enabled graphql fails, got %d", rr.Code)
	}
	if hits.Load() == 0 {
		t.Fatal("expected the GraphQL probe to run when not disabled")
	}
}
