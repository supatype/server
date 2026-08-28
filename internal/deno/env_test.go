package deno

import (
	"slices"
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
