package deno

import (
	"slices"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
)

func TestEdgeSubprocessEnv_coreAndPassThrough(t *testing.T) {
	t.Setenv("SUPATYPE_EDGE_CUSTOM", "hello")

	srv := &config.Config{
		SupatypeURL:    "",
		AnonKey:        "anon-jwt",
		ServiceRoleKey: "service-jwt",
	}
	got := EdgeSubprocessEnv(srv, "http://localhost:9999")

	if !slices.Contains(got, "CUSTOM=hello") {
		t.Fatalf("missing pass-through: %#v", got)
	}
	if !slices.Contains(got, "SUPATYPE_URL=http://localhost:9999") {
		t.Fatalf("missing URL: %#v", got)
	}
	if !slices.Contains(got, "SUPATYPE_ANON_KEY=anon-jwt") {
		t.Fatalf("missing anon: %#v", got)
	}
	if !slices.Contains(got, "SUPATYPE_SERVICE_ROLE_KEY=service-jwt") {
		t.Fatalf("missing service role: %#v", got)
	}
}

func TestEdgeSubprocessEnv_supatypeURLWinsOverFallback(t *testing.T) {
	srv := &config.Config{
		SupatypeURL:    "https://api.example",
		ServiceRoleKey: "x",
	}
	got := EdgeSubprocessEnv(srv, "http://ignored:1")
	for _, e := range got {
		if e == "SUPATYPE_URL=https://api.example" {
			return
		}
	}
	t.Fatalf("expected explicit SUPATYPE_URL, got %#v", got)
}

// Nothing configured means nothing to inject, and a nil Config is what a
// deployment with no edge functions passes.
func TestEdgeSubprocessEnvWithNoConfig(t *testing.T) {
	if got := EdgeSubprocessEnv(nil, "http://localhost:9999"); got != nil {
		t.Errorf("got %v, want nothing", got)
	}
}

// The prefix is stripped to make the variable name, so a variable that is only
// the prefix names nothing and is skipped rather than exported as "=value".
func TestEdgeSubprocessEnvSkipsAVariableWithNoNameAfterThePrefix(t *testing.T) {
	t.Setenv("SUPATYPE_EDGE_", "orphan")
	t.Setenv("SUPATYPE_EDGE_REAL", "kept")

	got := EdgeSubprocessEnv(&config.Config{}, "")
	for _, entry := range got {
		if strings.HasPrefix(entry, "=") {
			t.Errorf("an unnamed variable was exported: %q", entry)
		}
	}
	if !slices.Contains(got, "REAL=kept") {
		t.Errorf("got %v, want the named one kept", got)
	}
}

// Nothing configured and no fallback means no URL rather than an empty one,
// which a function would read as a valid address and fail against.
func TestEdgeSubprocessEnvOmitsWhatIsNotSet(t *testing.T) {
	got := EdgeSubprocessEnv(&config.Config{SupatypeURL: "  ", AnonKey: "  ", ServiceRoleKey: " "}, "   ")

	for _, entry := range got {
		for _, name := range []string{"SUPATYPE_URL=", "SUPATYPE_ANON_KEY=", "SUPATYPE_SERVICE_ROLE_KEY="} {
			if strings.HasPrefix(entry, name) {
				t.Errorf("%s was exported with nothing behind it: %q", name, entry)
			}
		}
	}
}
