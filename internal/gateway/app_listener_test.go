package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/publicenv"
)

func managedAppConfig() config.Config {
	return config.Config{
		Mode:              "managed",
		ManagedProjectRef: "p",
		AppAddr:           ":9998",
		SiteDir:           "/var/lib/supatype/site",
		SiteSourceURL:     "http://control-plane:4001",
		SiteMaxBytes:      1 << 20,
	}
}

// The app listener serves a project's public site, so it exists only on a managed tenant gateway
// that knows its project, with somewhere to keep files and somewhere to fetch them from. Anywhere
// else the server refuses to start rather than run with a listener that would do nothing or serve
// the wrong thing.
func TestValidateAppListener(t *testing.T) {
	for name, c := range map[string]struct {
		mutate  func(*config.Config)
		wantErr bool
	}{
		"a managed tenant gateway":               {func(*config.Config) {}, false},
		"no app address, no listener":            {func(c *config.Config) { *c = config.Config{Mode: "dev"} }, false},
		"standalone mode":                        {func(c *config.Config) { c.Mode = "standalone" }, true},
		"dev mode":                               {func(c *config.Config) { c.Mode = "dev" }, true},
		"the platform server":                    {func(c *config.Config) { c.ManagedProjectRef = "" }, true},
		"a blank project":                        {func(c *config.Config) { c.ManagedProjectRef = " " }, true},
		"no site dir":                            {func(c *config.Config) { c.SiteDir = "" }, true},
		"no source url":                          {func(c *config.Config) { c.SiteSourceURL = " " }, true},
		"no byte budget, the pointer carries it": {func(c *config.Config) { c.SiteMaxBytes = 0 }, false},
		"a negative byte budget":                 {func(c *config.Config) { c.SiteMaxBytes = -1 }, true},
	} {
		cfg := managedAppConfig()
		c.mutate(&cfg)
		if err := ValidateAppListener(&cfg); (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", name, err, c.wantErr)
		}
	}
}

// siteStub answers every path it is handed with a marker, so a test can tell the site handler
// answered and nothing else did.
func siteStub() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("site:" + r.URL.Path))
	})
}

func appRequest(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestTheAppListenerServesTheSiteAndThePublicEnv(t *testing.T) {
	cfg := managedAppConfig()
	cfg.AnonKey = "anon-key-value"
	cfg.SupatypeURL = "https://p.example.test"
	h := NewAppHandler(&cfg, siteStub())

	for _, path := range []string{"/", "/about", "/assets/app.js"} {
		if rec := appRequest(h, http.MethodGet, path); rec.Code != http.StatusTeapot || rec.Body.String() != "site:"+path {
			t.Errorf("%s: %d %q, want the site's answer", path, rec.Code, rec.Body.String())
		}
	}

	rec := appRequest(h, http.MethodGet, publicenv.JSONPath)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "anon-key-value") || strings.Contains(rec.Body.String(), "site:") {
		t.Errorf("%s: %d %q, want the public env", publicenv.JSONPath, rec.Code, rec.Body.String())
	}
	rec = appRequest(h, http.MethodGet, publicenv.ScriptPath)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "anon-key-value") {
		t.Errorf("%s: %d %q, want the public env script", publicenv.ScriptPath, rec.Code, rec.Body.String())
	}
}

// None of the API, admin or platform routes is mounted here: each of these paths reaches only the
// site handler, which is where a miss turns into a 404.
func TestTheAppListenerMountsNoAPI(t *testing.T) {
	cfg := managedAppConfig()
	missing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "site:"+r.URL.Path, http.StatusNotFound)
	})
	h := NewAppHandler(&cfg, missing)
	for _, path := range []string{
		"/admin/v1/users", "/sql", "/metadata", "/studio-admin/x", "/studio-config", "/admin/studio-members",
		"/preview-links/resolve", "/platform/v1/x", "/rest/v1/posts", "/auth/v1/token", "/storage/v1/bucket",
		"/health", "/hooks/v1/previous/x",
	} {
		rec := appRequest(h, http.MethodGet, path)
		if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != "site:"+path {
			t.Errorf("%s: %d %q, want only the site handler to answer", path, rec.Code, rec.Body.String())
		}
	}
}

// A manifest that becomes live with a site in it makes the syncer fetch that deployment and the app
// listener serve it, and a flip to another deployment is followed.
func TestTheAppListenerFollowsTheLiveManifestsSite(t *testing.T) {
	const token = "tok"
	bodies := map[string]string{"d1": "first", "d2": "second"}
	sha := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if r.Header.Get("Authorization") != "Bearer "+token || len(parts) < 6 || bodies[parts[4]] == "" {
			http.NotFound(w, r)
			return
		}
		body := bodies[parts[4]]
		if parts[5] == "manifest" {
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{
				{"path": "index.html", "sha256": sha(body), "size": len(body)},
			}})
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer cp.Close()

	cfg := managedAppConfig()
	cfg.SiteDir = t.TempDir()
	cfg.SiteSourceURL = cp.URL

	current := "d1"
	live := newLiveManifests(&proxy.RouteManifest{Schema: "public"})
	live.merge = func(_ context.Context, file *proxy.RouteManifest) (*proxy.RouteManifest, error) {
		out := proxy.CloneRouteManifest(file)
		out.Site = &proxy.SiteRef{DeploymentID: current, Version: current, Token: token}
		return out, nil
	}
	syncer, err := newSiteSyncer(&cfg, live)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go syncer.Run(ctx)
	app := NewAppHandler(&cfg, syncer.Handler())

	waitBody := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if rec := appRequest(app, http.MethodGet, "/"); rec.Body.String() == want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("the app listener never served %q (status %+v)", want, syncer.Status())
	}

	live.refresh()
	waitBody("first")
	current = "d2"
	live.refresh()
	waitBody("second")
}
