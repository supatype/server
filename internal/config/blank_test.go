package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A blank line such as SUPATYPE_APP_SPA_FALLBACK= in a project's .env must mean
// "not set", the way compose reads ${VAR:-default}. envconfig on its own tries
// to parse "" as the field's type and refuses to start.
func TestLoadTreatsABlankTypedSettingAsUnset(t *testing.T) {
	t.Setenv("SUPATYPE_APP_SPA_FALLBACK", "")
	t.Setenv("SUPATYPE_GRAPHQL_DISABLED", "  ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with blank typed settings: %v", err)
	}
	if !cfg.AppSPAFallback {
		t.Fatal("blank SUPATYPE_APP_SPA_FALLBACK should take its default, true")
	}
	if cfg.GraphQLDisabled {
		t.Fatal("blank SUPATYPE_GRAPHQL_DISABLED should stay false")
	}
	if v, ok := os.LookupEnv("SUPATYPE_APP_SPA_FALLBACK"); !ok || v != "" {
		t.Fatalf("Load must leave the process environment as it found it, got %q, %v", v, ok)
	}
}

// A blank string setting keeps its meaning: an explicitly blank
// SUPATYPE_DENO_PATH turns the Deno supervisor off rather than falling back
// to "deno".
func TestLoadKeepsABlankStringSetting(t *testing.T) {
	t.Setenv("SUPATYPE_DENO_PATH", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DenoPath != "" {
		t.Fatalf("DenoPath = %q, want blank", cfg.DenoPath)
	}
}

// The same line arriving through the project's .env, which is how native dev
// runs the server.
func TestLoadTreatsABlankTypedSettingFromDotEnvAsUnset(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SUPATYPE_APP_SPA_FALLBACK=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Registers a restore for whatever the process had, then removes it so the
	// .env file is what sets it.
	t.Setenv("SUPATYPE_APP_SPA_FALLBACK", "x")
	if err := os.Unsetenv("SUPATYPE_APP_SPA_FALLBACK"); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnv(dir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load after a .env with a blank typed setting: %v", err)
	}
	if !cfg.AppSPAFallback {
		t.Fatal("blank SUPATYPE_APP_SPA_FALLBACK from .env should take its default, true")
	}
}
