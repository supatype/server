package outerhealth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/auth/internal/serverconf"
)

func TestAttachHealthJSON(t *testing.T) {
	r := chi.NewRouter()
	cfg := &serverconf.ServerConfig{Mode: "dev"}
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
	cfg := &serverconf.ServerConfig{Mode: "dev"}
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

func TestProbePostgREST(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	if !probePostgREST(ts.URL, time.Second) {
		t.Fatal("expected probe success")
	}
	if probePostgREST("http://127.0.0.1:9", 100*time.Millisecond) {
		t.Fatal("expected probe failure on closed port")
	}
}
