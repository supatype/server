package outerhealth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/changelog"
	"github.com/supatype/server/internal/config"
)

// /health says which path after hooks take, so `supatype doctor` can tell an operator that the
// request-path fallback misses writes made outside the API. It is information, not readiness: the
// fallback still serves every API write.
func TestHealthReportsTheAfterHookPathWithoutAffectingReadiness(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer up.Close()

	router := chi.NewRouter()
	Attach(router, &config.Config{Mode: "dev"}, "v", func() ProbeConfig {
		return ProbeConfig{
			PostgRESTURL:    up.URL,
			GraphQLDisabled: true,
			AfterHooks: func() changelog.Status {
				return changelog.Status{Path: changelog.PathRequest, Reason: "wal_level is not logical"}
			},
		}
	})

	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body struct {
		Status     string `json:"status"`
		Components struct {
			AfterHooks struct {
				Path   string `json:"path"`
				Reason string `json:"reason"`
			} `json:"after_hooks"`
		} `json:"components"`
	}
	if err := json.Unmarshal(health.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Components.AfterHooks.Path != "request" || body.Components.AfterHooks.Reason != "wal_level is not logical" {
		t.Fatalf("after_hooks = %+v", body.Components.AfterHooks)
	}
	if body.Status != "ok" {
		t.Fatalf("the fallback path must not mark the server degraded, got %q", body.Status)
	}
}

func TestHealthOmitsAfterHooksWhenNoListenerIsWired(t *testing.T) {
	router := chi.NewRouter()
	Attach(router, &config.Config{}, "v", func() ProbeConfig { return ProbeConfig{} })
	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body struct {
		Components map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(health.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body.Components["after_hooks"]; ok {
		t.Fatal("no listener, no after_hooks component")
	}
}
