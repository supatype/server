package deno

import (
	"slices"
	"strings"
	"testing"
)

func TestEnvForDenoProcess_overridesPORT(t *testing.T) {
	t.Setenv("PORT", "9999")

	got := envForDenoProcess(8001, []string{"EXTRA=1"})

	if slices.ContainsFunc(got, func(s string) bool { return s == "PORT=9999" }) {
		t.Fatalf("old PORT=9999 should be stripped")
	}
	if !slices.Contains(got, "PORT=8001") {
		t.Fatalf("want PORT=8001")
	}
	if !slices.Contains(got, "EXTRA=1") {
		t.Fatalf("want EXTRA=1")
	}
}

// RB-2 (e2e 2026-11-21): the child used to get os.Environ() whole, so a function read the stack's
// signing secret, the service-role key, the database password and anything in the developer's shell
// through Deno.env. Only the allowlist and the explicit extras reach it.
func TestEnvForDenoProcess_dropsWhatIsNotAllowed(t *testing.T) {
	for _, key := range []string{
		"JWT_SECRET", "SUPATYPE_JWT_SECRET", "SERVICE_ROLE_KEY", "SUPATYPE_SERVICE_ROLE_KEY",
		"POSTGRES_PASSWORD", "DATABASE_URL", "AWS_SECRET_ACCESS_KEY", "SOME_SHELL_VARIABLE",
	} {
		t.Setenv(key, "secret")
	}

	got := envForDenoProcess(8001, nil)

	for _, entry := range got {
		if strings.HasSuffix(entry, "=secret") {
			t.Fatalf("%s reached the Deno child", entry[:strings.IndexByte(entry, '=')])
		}
	}
}

func TestEnvForDenoProcess_keepsTheAllowlist(t *testing.T) {
	t.Setenv("SUPATYPE_FUNCTIONS_ROOT", "/functions")
	t.Setenv("SUPATYPE_SERVICE_ROLE_KEY_FILE", "/key")
	t.Setenv("DENO_DIR", "/cache")
	t.Setenv("HTTPS_PROXY", "http://proxy")

	got := envForDenoProcess(8001, nil)

	for _, want := range []string{
		"SUPATYPE_FUNCTIONS_ROOT=/functions", "SUPATYPE_SERVICE_ROLE_KEY_FILE=/key",
		"DENO_DIR=/cache", "HTTPS_PROXY=http://proxy",
	} {
		if !slices.Contains(got, want) {
			t.Fatalf("want %s kept", want)
		}
	}
	if !slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(strings.ToUpper(s), "PATH=") }) {
		t.Fatalf("want PATH kept")
	}
}

// The extras are the server's own choice (EdgeSubprocessEnv), so they pass whatever their names.
func TestEnvForDenoProcess_keepsExplicitExtras(t *testing.T) {
	got := envForDenoProcess(8001, []string{"SUPATYPE_SERVICE_ROLE_KEY=k", "FOO=1"})

	if !slices.Contains(got, "SUPATYPE_SERVICE_ROLE_KEY=k") || !slices.Contains(got, "FOO=1") {
		t.Fatalf("want the extras kept")
	}
}

// The restart schedule has to grow and has to have a ceiling, or a crash loop
// either hammers the machine or gives up on ever coming back.
func TestBackoffConstants(t *testing.T) {
	t.Parallel()
	if backoffInitial <= 0 || backoffMax < backoffInitial {
		t.Fatalf("backoffInitial=%v backoffMax=%v", backoffInitial, backoffMax)
	}
}
