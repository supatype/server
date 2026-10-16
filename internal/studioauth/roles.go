package studioauth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// openWorkingDirectory is a seam. os.OpenRoot(".") fails only when the directory
// the process is running in has been removed underneath it, which is arrangeable
// on Linux and not on Windows.
var openWorkingDirectory = func() (*os.Root, error) { return os.OpenRoot(".") }

// DefaultAdminRoles are allowed Studio admin roles when config/env omit overrides.
var DefaultAdminRoles = []string{"admin", "supatype_admin"}

// AdminRolesFromOverride parses a comma-separated role override, or returns the
// defaults when it is empty. The value comes from configuration; this used to
// read STUDIO_ADMIN_ROLES itself.
func AdminRolesFromOverride(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return append([]string(nil), DefaultAdminRoles...)
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), DefaultAdminRoles...)
	}
	return out
}

type adminConfigRoles struct {
	AdminRoles []string `json:"adminRoles"`
}

// ReadAdminConfigFile reads admin-config.json from a relative path under the working directory.
func ReadAdminConfigFile(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, os.ErrNotExist
	}
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) {
		return nil, fmt.Errorf("admin config path must be relative")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("admin config path escapes working directory")
	}
	root, err := openWorkingDirectory()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(clean)
}

// AdminRolesFromConfigFile merges adminRoles from admin-config.json when present,
// over any comma-separated override supplied by configuration.
func AdminRolesFromConfigFile(path, override string) []string {
	roles := AdminRolesFromOverride(override)
	if strings.TrimSpace(path) == "" {
		return roles
	}
	data, err := ReadAdminConfigFile(path)
	if err != nil {
		return roles
	}
	var cfg adminConfigRoles
	if err := json.Unmarshal(data, &cfg); err != nil || len(cfg.AdminRoles) == 0 {
		return roles
	}
	return cfg.AdminRoles
}
