package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
)

const (
	publicEnvAnon    = "anon-key-for-public-env"
	publicEnvService = "service-role-key-must-never-leak"
	publicEnvURL     = "https://api.example.com"
)

// publicEnvMux builds the gateway for appMode. A static app's dist ships its
// own supatype-env.js and an index.html, so a test can tell which answered.
func publicEnvMux(t *testing.T, appMode string, mutate func(*config.Config)) http.Handler {
	t.Helper()
	clearAmbientEnv(t)
	dir := t.TempDir()
	for name, body := range map[string]string{
		"index.html":        "<!doctype html><title>spa</title>",
		"supatype-env.js":   "window.__FROM_DIST__ = true;",
		"supatype-env.json": `{"fromDist":true}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Mode:           "standalone",
		AppMode:        appMode,
		AppStaticDir:   dir,
		AppSPAFallback: true,
		AnonKey:        publicEnvAnon,
		ServiceRoleKey: publicEnvService,
		JWTSecret:      testJWTSecret,
	}
	if mutate != nil {
		mutate(cfg)
	}
	manifest := &proxy.RouteManifest{Schema: "public"}
	return buildOuterMux(
		cfg,
		func(*http.Request) *proxy.RouteManifest { return manifest },
		func() outerhealth.ProbeConfig { return outerhealth.ProbeConfigFrom(cfg, manifest, "") },
		http.NotFoundHandler(),
		nil,
		"publicenv-test",
		nil,
		nil,
		WithAPIExternalURL(publicEnvURL+"/"),
	)
}

func getPublicEnv(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Accept", "text/html,*/*")
	h.ServeHTTP(rec, req)
	return rec
}

// Both paths answer from the configuration, ahead of a file of the same name in
// the app's dist and of the SPA fallback, and with static hosting off.
func TestPublicEnvIsServedAtTheGatewayRoot(t *testing.T) {
	for _, appMode := range []string{"static", "none", "proxy"} {
		h := publicEnvMux(t, appMode, nil)
		js := getPublicEnv(h, http.MethodGet, "/supatype-env.js")
		wantJS := `window.__SUPATYPE_ENV__ = {"url":"` + publicEnvURL + `","anonKey":"` + publicEnvAnon + `"};` + "\n"
		if js.Code != http.StatusOK || js.Body.String() != wantJS {
			t.Errorf("%s: /supatype-env.js = %d %q, want 200 %q", appMode, js.Code, js.Body.String(), wantJS)
		}
		if got := js.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: /supatype-env.js Cache-Control = %q", appMode, got)
		}
		doc := getPublicEnv(h, http.MethodGet, "/supatype-env.json")
		wantJSON := `{"url":"` + publicEnvURL + `","anonKey":"` + publicEnvAnon + `"}`
		if doc.Code != http.StatusOK || doc.Body.String() != wantJSON {
			t.Errorf("%s: /supatype-env.json = %d %q, want 200 %q", appMode, doc.Code, doc.Body.String(), wantJSON)
		}
		if rec := getPublicEnv(h, http.MethodPost, "/supatype-env.js"); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: POST /supatype-env.js = %d, want 405", appMode, rec.Code)
		}
		if rec := getPublicEnv(h, http.MethodHead, "/supatype-env.json"); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Errorf("%s: HEAD /supatype-env.json = %d with %d body bytes", appMode, rec.Code, rec.Body.Len())
		}
		for path, rec := range map[string]*httptest.ResponseRecorder{"js": js, "json": doc} {
			if strings.Contains(rec.Body.String(), publicEnvService) {
				t.Errorf("%s %s: the service role key is in the body", appMode, path)
			}
		}
	}
}

// The neighbours still behave as before: the dist's other files and the SPA
// fallback are untouched by the two reserved paths.
func TestPublicEnvLeavesTheStaticAppAlone(t *testing.T) {
	h := publicEnvMux(t, "static", nil)
	if rec := getPublicEnv(h, http.MethodGet, "/some/route"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "spa") {
		t.Errorf("SPA fallback = %d %q", rec.Code, rec.Body.String())
	}
	if rec := getPublicEnv(h, http.MethodGet, "/supatype-env.js/extra"); strings.Contains(rec.Body.String(), "__SUPATYPE_ENV__") {
		t.Errorf("a path under the script answered with it: %q", rec.Body.String())
	}
}

func TestPublicEnvSwitchedOffIs404(t *testing.T) {
	h := publicEnvMux(t, "static", func(cfg *config.Config) { cfg.PublicEnv = config.SwitchedOff() })
	for _, path := range []string{"/supatype-env.js", "/supatype-env.json"} {
		rec := getPublicEnv(h, http.MethodGet, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", path, got)
		}
		if body := rec.Body.String(); strings.Contains(body, publicEnvAnon) || strings.Contains(body, "fromDist") || strings.Contains(body, "__FROM_DIST__") {
			t.Errorf("%s: switched off but answered %q", path, body)
		}
	}
}

func TestPublicEnvWithoutAnAnonKeyIs503(t *testing.T) {
	h := publicEnvMux(t, "static", func(cfg *config.Config) { cfg.AnonKey = "" })
	rec := getPublicEnv(h, http.MethodGet, "/supatype-env.json")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "SUPATYPE_ANON_KEY") {
		t.Errorf("status %d body %q, want 503 naming SUPATYPE_ANON_KEY", rec.Code, rec.Body.String())
	}
}

// SUPATYPE_URL wins over the auth service's external URL, the same precedence
// the edge functions are given.
func TestPublicEnvPrefersSupatypeURL(t *testing.T) {
	h := publicEnvMux(t, "none", func(cfg *config.Config) { cfg.SupatypeURL = "https://edge.example.com" })
	rec := getPublicEnv(h, http.MethodGet, "/supatype-env.json")
	if !strings.Contains(rec.Body.String(), `"url":"https://edge.example.com"`) {
		t.Errorf("body %q does not carry SUPATYPE_URL", rec.Body.String())
	}
}
