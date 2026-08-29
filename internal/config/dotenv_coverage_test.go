package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Where the service looks for .env files, and what it does when one of them is
// not readable. A dotenv that silently fails to load presents as a variable
// nobody set, a long way from the file that was meant to set it.

// writeEnv puts a .env file in dir and returns dir.
func writeEnv(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// ─── Where the project root is ────────────────────────────────────────────────

// The standard layout puts the manifest in .supatype under the project, so the
// project root is that directory's parent. Anything else is taken at face
// value: the directory holding the manifest.
func TestProjectRootFromManifestPath(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		path string
		want string
	}{
		"the standard layout": {
			filepath.Join("proj", ".supatype", "manifest.json"),
			filepath.Join(cwd, "proj"),
		},
		"a manifest somewhere else": {
			filepath.Join("elsewhere", "routes.json"),
			filepath.Join(cwd, "elsewhere"),
		},
		"nothing, so the default": {
			"", filepath.Join(cwd),
		},
		"padded": {
			"  " + filepath.Join("proj", ".supatype", "manifest.json") + "  ",
			filepath.Join(cwd, "proj"),
		},
	} {
		got, err := ProjectRootFromManifestPath(tc.path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != tc.want {
			t.Errorf("%s: root = %q, want %q", name, got, tc.want)
		}
	}
}

// A path that cannot be resolved is reported rather than guessed at.
func TestProjectRootWhenThePathCannotBeResolved(t *testing.T) {
	original := absPath
	t.Cleanup(func() { absPath = original })
	absPath = func(string) (string, error) { return "", errors.New("no working directory") }

	if _, err := ProjectRootFromManifestPath("anything"); err == nil {
		t.Error("want an error")
	}
	// A working directory to search reaches the failure first; with none, the
	// manifest-derived root is where it surfaces. Both have to report.
	if err := LoadDotEnvForServe("somewhere", ""); err == nil {
		t.Error("LoadDotEnvForServe with a working directory: want an error")
	}
	if err := LoadDotEnvForServe("", ""); err == nil {
		t.Error("LoadDotEnvForServe with no working directory: want an error")
	}
}

// ─── Which directories are searched ───────────────────────────────────────────

// A directory is searched at most once, however many ways it is named.
func TestADirectoryIsLoadedOnce(t *testing.T) {
	dir := writeEnv(t, t.TempDir(), ".env", "SUPATYPE_ONCE=first\n")
	t.Setenv("SUPATYPE_MANIFEST_PATH", filepath.Join(dir, ".supatype", "manifest.json"))

	// The config file's directory, the working directory and the
	// manifest-derived root are all this same directory.
	configFile := filepath.Join(dir, "server.env")
	if err := os.WriteFile(configFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := LoadDotEnvForServe(dir, configFile); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SUPATYPE_ONCE"); got != "first" {
		t.Errorf("SUPATYPE_ONCE = %q", got)
	}
	t.Cleanup(func() { _ = os.Unsetenv("SUPATYPE_ONCE") })
}

// No working directory named is not a directory to search, and not an error:
// the other locations still apply.
func TestAnEmptyDirectoryIsSkipped(t *testing.T) {
	dir := writeEnv(t, t.TempDir(), ".env", "SUPATYPE_FROM_MANIFEST_ROOT=yes\n")
	t.Setenv("SUPATYPE_MANIFEST_PATH", filepath.Join(dir, ".supatype", "manifest.json"))
	t.Cleanup(func() { _ = os.Unsetenv("SUPATYPE_FROM_MANIFEST_ROOT") })

	if err := LoadDotEnvForServe("   ", ""); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SUPATYPE_FROM_MANIFEST_ROOT"); got != "yes" {
		t.Errorf("SUPATYPE_FROM_MANIFEST_ROOT = %q", got)
	}
}

// A config path that is a directory, or is not there, is not a file to take a
// directory from.
func TestAConfigPathThatIsNotAFile(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, ".env", "SUPATYPE_FROM_CWD=yes\n")
	t.Cleanup(func() { _ = os.Unsetenv("SUPATYPE_FROM_CWD") })
	t.Setenv("SUPATYPE_MANIFEST_PATH", filepath.Join(dir, ".supatype", "manifest.json"))

	for name, configPath := range map[string]string{
		"a directory":   dir,
		"nothing there": filepath.Join(dir, "absent.env"),
		"empty":         "",
		"whitespace":    "   ",
	} {
		if err := LoadDotEnvForServe(dir, configPath); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := os.Getenv("SUPATYPE_FROM_CWD"); got != "yes" {
		t.Errorf("SUPATYPE_FROM_CWD = %q", got)
	}
}

// ─── Files that will not load ─────────────────────────────────────────────────

// A .env that does not parse is reported. Continuing would present as a
// variable nobody set, a long way from the file meant to set it.
func TestAnUnparseableDotEnvIsReported(t *testing.T) {
	for _, name := range []string{".env", ".env.local"} {
		dir := writeEnv(t, t.TempDir(), name, "SUPATYPE_BROKEN=\"unterminated\n")
		if err := LoadDotEnv(dir); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// And the failure travels out of every place that searches for one.
func TestAnUnparseableDotEnvStopsTheSearch(t *testing.T) {
	broken := writeEnv(t, t.TempDir(), ".env", "SUPATYPE_BROKEN=\"unterminated\n")
	t.Setenv("SUPATYPE_MANIFEST_PATH", filepath.Join(t.TempDir(), ".supatype", "manifest.json"))

	configFile := filepath.Join(broken, "server.env")
	if err := os.WriteFile(configFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := LoadDotEnvForServe(t.TempDir(), configFile); err == nil {
		t.Error("a broken file in the config directory did not stop the search")
	}
	if err := LoadDotEnvForServe(broken, ""); err == nil {
		t.Error("a broken file in the working directory did not stop the search")
	}

	t.Setenv("SUPATYPE_MANIFEST_PATH", filepath.Join(broken, ".supatype", "manifest.json"))
	if err := LoadDotEnvForServe(t.TempDir(), ""); err == nil {
		t.Error("a broken file at the manifest root did not stop the search")
	}
}

// A directory with no .env files is not a failure; most projects have none.
func TestADirectoryWithNoFiles(t *testing.T) {
	if err := LoadDotEnv(t.TempDir()); err != nil {
		t.Errorf("%v", err)
	}
}

// ─── Precedence ───────────────────────────────────────────────────────────────

// The shell wins over both files, and .env.local wins over .env. A developer
// exporting a variable to try something must not have it overwritten by a file
// they forgot about.
func TestPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, ".env.local", "SUPATYPE_PREC_A=from-local\nSUPATYPE_PREC_B=from-local\n")
	writeEnv(t, dir, ".env", "SUPATYPE_PREC_A=from-base\nSUPATYPE_PREC_C=from-base\n")

	t.Setenv("SUPATYPE_PREC_B", "from-shell")
	t.Cleanup(func() {
		_ = os.Unsetenv("SUPATYPE_PREC_A")
		_ = os.Unsetenv("SUPATYPE_PREC_C")
	})

	if err := LoadDotEnv(dir); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ key, want string }{
		"local beats base":     {"SUPATYPE_PREC_A", "from-local"},
		"the shell beats both": {"SUPATYPE_PREC_B", "from-shell"},
		"base fills the rest":  {"SUPATYPE_PREC_C", "from-base"},
	} {
		if got := os.Getenv(tc.key); got != tc.want {
			t.Errorf("%s: %s = %q, want %q", name, tc.key, got, tc.want)
		}
	}
}

// The search order is config directory, then working directory, then the
// manifest root: the manifest path itself may be set by the working
// directory's own .env, so it is read last.
func TestTheSearchOrder(t *testing.T) {
	configDir := writeEnv(t, filepath.Join(t.TempDir(), "cfg"), ".env", "SUPATYPE_ORDER=config-dir\n")
	cwd := writeEnv(t, filepath.Join(t.TempDir(), "cwd"), ".env", "SUPATYPE_ORDER=cwd\n")
	root := writeEnv(t, filepath.Join(t.TempDir(), "root"), ".env", "SUPATYPE_ORDER=manifest-root\n")

	configFile := filepath.Join(configDir, "server.env")
	if err := os.WriteFile(configFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUPATYPE_MANIFEST_PATH", filepath.Join(root, ".supatype", "manifest.json"))
	t.Cleanup(func() { _ = os.Unsetenv("SUPATYPE_ORDER") })

	if err := LoadDotEnvForServe(cwd, configFile); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SUPATYPE_ORDER"); got != "config-dir" {
		t.Errorf("SUPATYPE_ORDER = %q, want the config directory to win", got)
	}
}

// Nothing here logs what it read: these files hold the JWT secret and the
// database password.
func TestLoadingDoesNotReturnFileContents(t *testing.T) {
	dir := writeEnv(t, t.TempDir(), ".env", "SUPATYPE_JWT_SECRET=super-secret\n")
	t.Cleanup(func() { _ = os.Unsetenv("SUPATYPE_JWT_SECRET") })

	err := LoadDotEnv(dir)
	if err != nil && strings.Contains(err.Error(), "super-secret") {
		t.Errorf("the error carried the file's contents: %v", err)
	}
}
