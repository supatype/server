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

	roles := AdminRolesFromConfigFile(".supatype/admin-config.json")
	if len(roles) != 2 || roles[0] != "editor" || roles[1] != "ops" {
		t.Fatalf("unexpected roles: %#v", roles)
	}
}
