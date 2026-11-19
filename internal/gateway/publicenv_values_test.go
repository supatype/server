package gateway

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/publicenv"
)

// publicValuesMux is the gateway answering from whichever manifest `current` holds.
func publicValuesMux(t *testing.T, current *atomic.Pointer[proxy.RouteManifest]) http.Handler {
	t.Helper()
	clearAmbientEnv(t)
	cfg := &config.Config{Mode: "standalone", AppMode: "none", AnonKey: publicEnvAnon, ServiceRoleKey: publicEnvService, JWTSecret: testJWTSecret}
	return buildOuterMux(
		cfg,
		func(*http.Request) *proxy.RouteManifest { return current.Load() },
		func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, current.Load(), "") },
		http.NotFoundHandler(),
		nil,
		"publicenv-test",
		nil,
		nil,
		WithAPIExternalURL(publicEnvURL),
	)
}

// The values a project declares are served beside the address and anon key, and a change to the
// manifest reaches the next load with no restart.
func TestPublicEnvServesTheManifestsValues(t *testing.T) {
	var current atomic.Pointer[proxy.RouteManifest]
	current.Store(&proxy.RouteManifest{Schema: "public", PublicEnv: map[string]string{"STRIPE_PK": "pk_live_1"}})
	h := publicValuesMux(t, &current)

	doc := getPublicEnv(h, http.MethodGet, publicenv.JSONPath)
	want := `{"url":"` + publicEnvURL + `","anonKey":"` + publicEnvAnon + `","env":{"STRIPE_PK":"pk_live_1"}}`
	if doc.Code != http.StatusOK || doc.Body.String() != want {
		t.Fatalf("%s = %d %q, want %q", publicenv.JSONPath, doc.Code, doc.Body.String(), want)
	}
	js := getPublicEnv(h, http.MethodGet, publicenv.ScriptPath)
	if !strings.Contains(js.Body.String(), `"env":{"STRIPE_PK":"pk_live_1"}`) {
		t.Errorf("script = %q", js.Body.String())
	}

	current.Store(&proxy.RouteManifest{Schema: "public", PublicEnv: map[string]string{"STRIPE_PK": "pk_live_2"}})
	if got := getPublicEnv(h, http.MethodGet, publicenv.JSONPath).Body.String(); !strings.Contains(got, "pk_live_2") {
		t.Errorf("after a manifest change = %q, want the new value", got)
	}
}

// The app listener, which has no manifest of its own, is handed the live one's values.
func TestTheAppListenerServesThePublicValues(t *testing.T) {
	cfg := managedAppConfig()
	cfg.AnonKey = "anon-key-value"
	cfg.SupatypeURL = "https://p.example.test"
	source := func(*http.Request) map[string]string { return map[string]string{"MAPS_KEY": "m1"} }
	h := NewAppHandler(&cfg, siteStub(), WithPublicEnvSource(source))
	rec := appRequest(h, http.MethodGet, publicenv.JSONPath)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"env":{"MAPS_KEY":"m1"}`) {
		t.Errorf("%s: %d %q", publicenv.JSONPath, rec.Code, rec.Body.String())
	}
}
