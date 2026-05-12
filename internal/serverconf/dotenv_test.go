package serverconf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectRootFromManifestPath_underSupatype(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sup := filepath.Join(dir, "app", ".supatype")
	if err := os.MkdirAll(sup, 0o755); err != nil {
		t.Fatal(err)
	}
	man := filepath.Join(sup, "manifest.json")
	if err := os.WriteFile(man, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ProjectRootFromManifestPath(man)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "app")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestLoadDotEnvForServe_secondProjectRoot(t *testing.T) {
	cwd := t.TempDir()
	app := filepath.Join(cwd, "app")
	sup := filepath.Join(app, ".supatype")
	if err := os.MkdirAll(sup, 0o755); err != nil {
		t.Fatal(err)
	}
	man := filepath.Join(sup, "manifest.json")
	if err := os.WriteFile(man, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, ".env"), []byte("FROM_PROJ=ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// cwd/.env only points at manifest path; project .env supplies the rest.
	manifestLine := "SUPATYPE_MANIFEST_PATH=" + man + "\n"
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte(manifestLine), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv("FROM_PROJ")
		_ = os.Unsetenv("SUPATYPE_MANIFEST_PATH")
	})
	_ = os.Unsetenv("FROM_PROJ")
	_ = os.Unsetenv("SUPATYPE_MANIFEST_PATH")

	if err := LoadDotEnvForServe(cwd, ""); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FROM_PROJ") != "ok" {
		t.Fatalf("FROM_PROJ=%q", os.Getenv("FROM_PROJ"))
	}
	gotMan := filepath.Clean(os.Getenv("SUPATYPE_MANIFEST_PATH"))
	wantMan := filepath.Clean(man)
	if gotMan != wantMan {
		t.Fatalf("SUPATYPE_MANIFEST_PATH=%q want %q", gotMan, wantMan)
	}
}

func TestLoadDotEnvForServe_configFileDir(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "api.env")
	if err := os.WriteFile(cfgPath, []byte("# config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, ".env"), []byte("FROM_CFG=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	t.Cleanup(func() { _ = os.Unsetenv("FROM_CFG") })
	_ = os.Unsetenv("FROM_CFG")

	if err := LoadDotEnvForServe(cwd, cfgPath); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FROM_CFG") != "1" {
		t.Fatalf("FROM_CFG=%q", os.Getenv("FROM_CFG"))
	}
}

func TestLoadDotEnv_processEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("FOO=from_file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FOO", "from_shell")
	if err := LoadDotEnv(dir); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FOO") != "from_shell" {
		t.Fatalf("FOO=%q want from_shell", os.Getenv("FOO"))
	}
}

func TestLoadDotEnv_localOverridesBase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("FOO=from_base\nBAR=only_base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("FOO=from_local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("FOO"); _ = os.Unsetenv("BAR") })
	_ = os.Unsetenv("FOO")
	_ = os.Unsetenv("BAR")

	if err := LoadDotEnv(dir); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FOO") != "from_local" {
		t.Fatalf("FOO=%q want from_local", os.Getenv("FOO"))
	}
	if os.Getenv("BAR") != "only_base" {
		t.Fatalf("BAR=%q want only_base", os.Getenv("BAR"))
	}
}
