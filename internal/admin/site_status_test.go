package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace/keyspacetest"
	"github.com/supatype/server/internal/restcache"
	"github.com/supatype/server/internal/static"
)

// Studio's Static site pages say what the app mount serves. These tests hold the route that tells
// them to what the deployment is configured with and what is on disk.

func siteHandler(t *testing.T, cfg *config.Config, site SiteSettings) http.Handler {
	t.Helper()
	cfg.ServiceRoleKey = serviceKey
	ks := keyspacetest.New()
	return Handler(Deps{
		Store: newMemStore(), Config: cfg, Cache: ks, Platform: ks, Stats: restcache.NewCounter(),
		Declared: permissiveCeiling,
		Site:     func(*http.Request) SiteSettings { return site },
	})
}

func writeSiteFile(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func siteStatusOf(t *testing.T, h http.Handler) siteStatusResponse {
	t.Helper()
	rec := call(t, h, http.MethodGet, "/site/status", serviceKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var got siteStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSiteStatusReportsAStaticDirectory(t *testing.T) {
	dir := t.TempDir()
	writeSiteFile(t, dir, "index.html", "<html></html>")      // 13 bytes
	writeSiteFile(t, dir, "assets/app.js", "console.log(1);") // 15 bytes
	h := siteHandler(t, &config.Config{Mode: "standalone"},
		SiteSettings{Mode: "static", StaticDir: dir, SPAFallback: true})

	got := siteStatusOf(t, h)
	if got.Mode != "static" || got.StaticDir != dir || !got.SPAFallback {
		t.Errorf("settings = %+v", got)
	}
	if !got.Serving || got.FileCount != 2 || got.TotalBytes != 28 {
		t.Errorf("serving=%v files=%d bytes=%d, want true 2 28", got.Serving, got.FileCount, got.TotalBytes)
	}
}

func TestSiteStatusStaticWithNothingToServeIsNotServing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "dist")
	got := siteStatusOf(t, siteHandler(t, &config.Config{Mode: "standalone"},
		SiteSettings{Mode: "static", StaticDir: missing}))
	if got.Serving || got.FileCount != 0 {
		t.Errorf("a missing directory is serving=%v files=%d", got.Serving, got.FileCount)
	}
}

func TestSiteStatusReportsTheCacheControlInForce(t *testing.T) {
	cache := static.CacheOpts{HTML: "no-store", Prefixes: map[string]string{"/downloads/": "public, max-age=60"}}.Effective()
	got := siteStatusOf(t, siteHandler(t, &config.Config{Mode: "standalone"},
		SiteSettings{Mode: "static", StaticDir: t.TempDir(), Cache: cache}))
	if got.CacheControl.HTML != "no-store" {
		t.Errorf("html = %q", got.CacheControl.HTML)
	}
	if got.CacheControl.HashedAssets != "public, max-age=31536000, immutable" {
		t.Errorf("hashed assets = %q, want the built-in default", got.CacheControl.HashedAssets)
	}
	if got.CacheControl.Prefixes["/downloads/"] != "public, max-age=60" {
		t.Errorf("prefixes = %v", got.CacheControl.Prefixes)
	}
}

func TestSiteStatusReportsAProxyUpstream(t *testing.T) {
	got := siteStatusOf(t, siteHandler(t, &config.Config{Mode: "standalone"},
		SiteSettings{Mode: "proxy", Upstream: "http://localhost:5173"}))
	if got.Mode != "proxy" || got.Upstream != "http://localhost:5173" || !got.Serving {
		t.Errorf("proxy status = %+v", got)
	}
	if got.StaticDir != "" || got.FileCount != 0 {
		t.Errorf("a proxy has no directory: %+v", got)
	}
}

func TestSiteStatusWithNoAppIsModeNone(t *testing.T) {
	// No Site resolver at all, as every deployment mounted before this route existed.
	cfg := &config.Config{Mode: "standalone", ServiceRoleKey: serviceKey}
	ks := keyspacetest.New()
	h := Handler(Deps{Store: newMemStore(), Config: cfg, Cache: ks, Platform: ks, Stats: restcache.NewCounter()})
	got := siteStatusOf(t, h)
	if got.Mode != "none" || got.Serving {
		t.Errorf("status = %+v, want mode none and not serving", got)
	}
}

func TestSiteStatusRefusesWithoutTheServiceRoleKey(t *testing.T) {
	h := siteHandler(t, &config.Config{Mode: "standalone"}, SiteSettings{Mode: "static", StaticDir: t.TempDir()})
	for name, bearer := range map[string]string{"no key": "", "wrong key": "not-the-key"} {
		rec := call(t, h, http.MethodGet, "/site/status", bearer, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, rec.Code)
		}
	}
}

func TestSiteStatusIsReadOnly(t *testing.T) {
	h := siteHandler(t, &config.Config{Mode: "standalone"}, SiteSettings{Mode: "none"})
	rec := call(t, h, http.MethodPost, "/site/status", serviceKey, "{}")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
}
