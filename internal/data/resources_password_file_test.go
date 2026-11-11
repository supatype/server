package data

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace"
)

// writePasswordFile writes content to a file in a test directory and returns
// its path.
func writePasswordFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// loadEnv loads the configuration from env, as the server does at start.
func loadEnv(t *testing.T, env map[string]string) *config.Config {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A password read from a file reaches each keyspace exactly as a password set
// directly would, one trailing newline stripped, each from its own file.
func TestOpenSendsPasswordsReadFromFiles(t *testing.T) {
	seen := stubKeyspace(t)
	cfg := loadEnv(t, map[string]string{
		"SUPATYPE_KEYSPACE_ADDR":                  "postgres-platform:6379",
		"SUPATYPE_PROJECT_KEYSPACE_ADDR":          "postgres:6379",
		"SUPATYPE_KEYSPACE_PASSWORD_FILE":         writePasswordFile(t, "platform", "platform-pw\n"),
		"SUPATYPE_PROJECT_KEYSPACE_PASSWORD_FILE": writePasswordFile(t, "project", "project-pw\n"),
	})
	r, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	assertAuth(t, "platform", seen["postgres-platform:6379"],
		keyspace.Auth{Username: config.DefaultKeyspaceUsername, Password: "platform-pw"})
	assertAuth(t, "project", seen["postgres:6379"],
		keyspace.Auth{Username: config.DefaultKeyspaceUsername, Password: "project-pw"})
}

// A platform password file never reaches a project keyspace at its own address.
func TestAPlatformPasswordFileNeverReachesTheProjectKeyspace(t *testing.T) {
	seen := stubKeyspace(t)
	cfg := loadEnv(t, map[string]string{
		"SUPATYPE_KEYSPACE_ADDR":          "postgres-platform:6379",
		"SUPATYPE_PROJECT_KEYSPACE_ADDR":  "postgres:6379",
		"SUPATYPE_KEYSPACE_PASSWORD_FILE": writePasswordFile(t, "platform", "platform-pw\n"),
	})
	r, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	assertAuth(t, "project", seen["postgres:6379"], keyspace.Auth{})
}

// Each of these stops the start before anything is dialled, with a line that
// says what to fix and never carries a password.
func TestOpenRefusesBadPasswordFiles(t *testing.T) {
	const secret = "file-secret-b-ksrv"
	missing := filepath.Join(t.TempDir(), "absent")
	for _, tc := range []struct {
		name  string
		env   func() map[string]string
		wants []string
	}{
		{"a password and its file", func() map[string]string {
			return map[string]string{
				"SUPATYPE_KEYSPACE_PASSWORD":      "direct-b-ksrv",
				"SUPATYPE_KEYSPACE_PASSWORD_FILE": writePasswordFile(t, "p", secret),
			}
		}, []string{"SUPATYPE_KEYSPACE_PASSWORD", "SUPATYPE_KEYSPACE_PASSWORD_FILE"}},
		{"an unreadable file", func() map[string]string {
			return map[string]string{"SUPATYPE_PROJECT_KEYSPACE_PASSWORD_FILE": missing}
		}, []string{"SUPATYPE_PROJECT_KEYSPACE_PASSWORD_FILE", missing}},
		{"the same password in both files", func() map[string]string {
			return map[string]string{
				"SUPATYPE_KEYSPACE_PASSWORD_FILE":         writePasswordFile(t, "a", secret+"\n"),
				"SUPATYPE_PROJECT_KEYSPACE_PASSWORD_FILE": writePasswordFile(t, "b", secret),
			}
		}, []string{"must differ"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := stubKeyspace(t)
			env := tc.env()
			env["SUPATYPE_KEYSPACE_ADDR"], env["SUPATYPE_PROJECT_KEYSPACE_ADDR"] = "postgres-platform:6379", "postgres:6379"
			_, err := Open(context.Background(), loadEnv(t, env))
			if err == nil {
				t.Fatal("want a refusal")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should mention %q", err, want)
				}
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "direct-b-ksrv") {
				t.Errorf("the error carries a password: %v", err)
			}
			if len(seen) != 0 {
				t.Errorf("%d keyspaces dialled before the refusal", len(seen))
			}
		})
	}
}
