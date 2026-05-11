package serverconf

import (
	"os"
	"path/filepath"
	"testing"
)

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
