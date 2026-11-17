package outerhealth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/site"
)

// A site that has not synced, or failed to, is reported on /health and never makes the server
// not-ready: the API does not depend on the site.
func TestHealthReportsTheSiteWithoutAffectingReadiness(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer up.Close()

	router := chi.NewRouter()
	Attach(router, &config.Config{Mode: "dev"}, "v", func() ProbeConfig {
		return ProbeConfig{
			PostgRESTURL:    up.URL,
			GraphQLDisabled: true,
			Site: func() site.Status {
				return site.Status{Want: "d2", Serving: "d1", Error: "control plane answered 502"}
			},
		}
	})

	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body struct {
		Status     string `json:"status"`
		Components struct {
			Site site.Status `json:"site"`
		} `json:"components"`
	}
	if err := json.Unmarshal(health.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if got := body.Components.Site; got.Want != "d2" || got.Serving != "d1" || got.Error == "" {
		t.Fatalf("site = %+v", got)
	}
	if body.Status != "ok" {
		t.Fatalf("a failing site sync must not mark the server degraded, got %q", body.Status)
	}
}

func TestHealthOmitsTheSiteWhenThereIsNoAppListener(t *testing.T) {
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
	if _, ok := body.Components["site"]; ok {
		t.Fatal("no app listener, no site component")
	}
}
