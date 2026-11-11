package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// passwordFile writes content to a file in a test directory and returns its path.
func passwordFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// One trailing newline is not part of the password; everything else is.
func TestAPasswordFileLosesOneTrailingNewlineOnly(t *testing.T) {
	for content, want := range map[string]string{
		"pw":     "pw",
		"pw\n":   "pw",
		"pw\r\n": "pw",
		"pw\n\n": "pw\n",
		" pw \n": " pw ",
		"pw\r":   "pw\r",
		"p\nw\n": "p\nw",
	} {
		cfg := &Config{KeyspacePasswordFile: passwordFile(t, content), ProjectKeyspacePasswordFile: passwordFile(t, "other")}
		platform, project, err := cfg.LoadKeyspaceCredentials()
		if err != nil {
			t.Fatalf("%q: %v", content, err)
		}
		if got := platform.Password.Reveal(); got != want {
			t.Errorf("%q: read %q, want %q", content, got, want)
		}
		if got := project.Password.Reveal(); got != "other" {
			t.Errorf("project file read %q", got)
		}
	}
}

// What a file cannot be, each refused with the variables or path to fix and
// never a password.
func TestLoadKeyspaceCredentialsRefusesBadPasswordFiles(t *testing.T) {
	const secret = "secret-b-ksrv"
	missing := filepath.Join(t.TempDir(), "absent")
	for _, tc := range []struct {
		name  string
		cfg   *Config
		wants []string
	}{
		{"platform password and file", &Config{KeyspacePassword: secret, KeyspacePasswordFile: passwordFile(t, "x")},
			[]string{"set SUPATYPE_KEYSPACE_PASSWORD or SUPATYPE_KEYSPACE_PASSWORD_FILE, not both"}},
		{"project password and file", &Config{ProjectKeyspacePassword: secret, ProjectKeyspacePasswordFile: passwordFile(t, "x")},
			[]string{"set SUPATYPE_PROJECT_KEYSPACE_PASSWORD or SUPATYPE_PROJECT_KEYSPACE_PASSWORD_FILE, not both"}},
		{"unreadable platform file", &Config{KeyspacePasswordFile: missing},
			[]string{"SUPATYPE_KEYSPACE_PASSWORD_FILE names " + missing + ", which cannot be read"}},
		{"unreadable project file", &Config{ProjectKeyspacePasswordFile: missing},
			[]string{"SUPATYPE_PROJECT_KEYSPACE_PASSWORD_FILE names " + missing + ", which cannot be read"}},
		{"empty file", &Config{KeyspacePasswordFile: passwordFile(t, "\n")}, []string{"which holds no password"}},
		{"same password from two files", &Config{
			KeyspacePasswordFile: passwordFile(t, secret+"\n"), ProjectKeyspacePasswordFile: passwordFile(t, secret),
		}, []string{"must differ"}},
		{"same password, file and variable", &Config{
			KeyspacePasswordFile: passwordFile(t, secret), ProjectKeyspacePassword: secret,
		}, []string{"must differ"}},
	} {
		_, _, err := tc.cfg.LoadKeyspaceCredentials()
		if err == nil {
			t.Errorf("%s: want a refusal", tc.name)
			continue
		}
		for _, want := range tc.wants {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q should mention %q", tc.name, err, want)
			}
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: the error carries the password: %v", tc.name, err)
		}
	}
}

// A password read from a file is masked like one set directly.
func TestAPasswordReadFromAFileIsMasked(t *testing.T) {
	const secret = "from-file-b-ksrv"
	cfg := &Config{KeyspacePasswordFile: passwordFile(t, secret), ProjectKeyspacePasswordFile: passwordFile(t, "p-"+secret)}
	platform, project, err := cfg.LoadKeyspaceCredentials()
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if out := fmt.Sprintf(verb, []KeyspaceCredentials{platform, project}); strings.Contains(out, secret) {
			t.Errorf("%s prints the password", verb)
		}
	}
}
