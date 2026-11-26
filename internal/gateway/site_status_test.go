package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// The admin site status reads the app settings the way buildApp does: the route manifest over the
// config. A manifest's mode must win, or Studio would describe a mount that is not the one serving.
func TestSiteSettingsFollowTheManifestOverTheConfig(t *testing.T) {
	d := &Deps{
		Config: &config.Config{AppMode: "none", AppSPAFallback: true},
		ManifestFor: func(*http.Request) *proxy.RouteManifest {
			return &proxy.RouteManifest{AppMode: "static", AppStaticDir: "/srv/dist"}
		},
	}
	got := d.SiteSettings(httptest.NewRequest(http.MethodGet, "/", nil))
	if got.Mode != "static" || got.StaticDir != "/srv/dist" || !got.SPAFallback {
		t.Errorf("settings = %+v", got)
	}
}

func TestSiteSettingsWithNoManifestUseTheConfig(t *testing.T) {
	d := &Deps{Config: &config.Config{AppMode: "proxy", AppUpstream: "http://localhost:3001"}}
	got := d.SiteSettings(httptest.NewRequest(http.MethodGet, "/", nil))
	if got.Mode != "proxy" || got.Upstream != "http://localhost:3001" {
		t.Errorf("settings = %+v", got)
	}
}

// The admin site status says whether the config set the SPA fallback, from the same config.
func TestSiteSettingsCarryWhetherTheConfigSetTheSPAFallback(t *testing.T) {
	d := &Deps{Config: &config.Config{AppMode: "static", AppSPAFallback: true, AppSPAFallbackFromConfig: true}}
	if got := d.SiteSettings(httptest.NewRequest(http.MethodGet, "/", nil)); !got.SPAFallbackFromConfig {
		t.Errorf("settings = %+v", got)
	}
	d = &Deps{Config: &config.Config{AppMode: "static", AppSPAFallback: true}}
	if got := d.SiteSettings(httptest.NewRequest(http.MethodGet, "/", nil)); got.SPAFallbackFromConfig {
		t.Errorf("settings = %+v", got)
	}
}
