package admin

import (
	"encoding/json"
	"testing"
)

// Studio labels the SPA fallback "Set in supatype.config.ts" only when the status says the config
// set it.
func TestSiteStatusSaysWhetherTheConfigSetTheSPAFallback(t *testing.T) {
	for _, fromConfig := range []bool{true, false} {
		got := describeSite(SiteSettings{Mode: "static", StaticDir: t.TempDir(), SPAFallback: true, SPAFallbackFromConfig: fromConfig})
		if got.SPAFallbackFromConfig != fromConfig {
			t.Errorf("fromConfig %v: response = %v", fromConfig, got.SPAFallbackFromConfig)
		}
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]any
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		if v, ok := keys["spa_fallback_from_config"]; !ok || v != fromConfig {
			t.Errorf("fromConfig %v: json key = %v (present %v)", fromConfig, v, ok)
		}
	}
}

func TestSiteStatusLeavesTheSPAFallbackSourceOffAnAppThatIsNotStatic(t *testing.T) {
	got := describeSite(SiteSettings{Mode: "proxy", Upstream: "http://localhost:3000", SPAFallbackFromConfig: true})
	if got.SPAFallbackFromConfig {
		t.Error("a proxied app has no SPA fallback to label")
	}
}
