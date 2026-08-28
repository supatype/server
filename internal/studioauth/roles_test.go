package studioauth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadAdminConfigFile_rejectsTraversal(t *testing.T) {
	_, err := ReadAdminConfigFile("../outside.json")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestReadAdminConfigFile_readsRelativePath(t *testing.T) {
	dir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	cfgDir := filepath.Join(".supatype")
	if err := os.MkdirAll(cfgDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "admin-config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"adminRoles":["editor"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := ReadAdminConfigFile(".supatype/admin-config.json")
	if err != nil {
		t.Fatalf("ReadAdminConfigFile: %v", err)
	}
	if string(data) != `{"adminRoles":["editor"]}` {
		t.Fatalf("unexpected contents: %s", string(data))
	}
}

func TestAdminRolesFromConfigFile_mergesRoles(t *testing.T) {
	dir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	cfgDir := filepath.Join(".supatype")
	if err := os.MkdirAll(cfgDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "admin-config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"adminRoles":["editor","ops"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	roles := AdminRolesFromConfigFile(".supatype/admin-config.json", "")
	if len(roles) != 2 || roles[0] != "editor" || roles[1] != "ops" {
		t.Fatalf("unexpected roles: %#v", roles)
	}
}

// The open-Studio bypass answers unauthenticated requests and injects the service
// role key, so it must not survive a config being copied to a public deployment.
func TestDevBypassRequiresLocalAddress(t *testing.T) {
	cases := []struct {
		name        string
		externalURL string
		want        bool
	}{
		{"unset is treated as local", "", true},
		{"localhost", "http://localhost:18473", true},
		{"loopback ip", "http://127.0.0.1:18473", true},
		{"lvh.me", "http://api.lvh.me:18480", true},
		{"docker host", "http://host.docker.internal:18473", true},
		{"public domain", "https://api.example.com", false},
		{"public domain with path", "https://demo.supatype.com/auth", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("API_EXTERNAL_URL", tc.externalURL)
			t.Setenv("GOTRUE_API_EXTERNAL_URL", "")
			t.Setenv("GOTRUE_SITE_URL", "")
			c := Config{Mode: "dev", OpenDev: true}
			if got := c.DevBypass(); got != tc.want {
				t.Fatalf("DevBypass() with %q = %v, want %v", tc.externalURL, got, tc.want)
			}
		})
	}
}

func TestDevBypassStillRequiresBothFlags(t *testing.T) {
	t.Setenv("API_EXTERNAL_URL", "http://localhost:18473")

	if (Config{Mode: "standalone", OpenDev: true}).DevBypass() {
		t.Fatal("bypass must never apply outside dev mode")
	}
	if (Config{Mode: "dev", OpenDev: false}).DevBypass() {
		t.Fatal("bypass must require the explicit opt-in flag")
	}
	if !(Config{Mode: "dev", OpenDev: true}).DevBypass() {
		t.Fatal("both flags plus a local URL should enable the bypass")
	}
}
