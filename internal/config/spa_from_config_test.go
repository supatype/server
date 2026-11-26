package config

import "testing"

// The CLI says whether the project's config set the SPA fallback (build.spa), so Studio can label
// the value as the config's and not as a default.
func TestLoadReadsWhetherTheConfigSetTheSPAFallback(t *testing.T) {
	t.Setenv("SUPATYPE_APP_SPA_FALLBACK_FROM_CONFIG", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.AppSPAFallbackFromConfig {
		t.Fatal("SUPATYPE_APP_SPA_FALLBACK_FROM_CONFIG=true should be read")
	}
}

func TestLoadTakesTheSPAFallbackAsNotFromTheConfigByDefault(t *testing.T) {
	t.Setenv("SUPATYPE_APP_SPA_FALLBACK_FROM_CONFIG", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AppSPAFallbackFromConfig {
		t.Fatal("blank SUPATYPE_APP_SPA_FALLBACK_FROM_CONFIG should be false")
	}
}
