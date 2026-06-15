package studioauth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultAdminRoles are allowed Studio admin roles when config/env omit overrides.
var DefaultAdminRoles = []string{"admin", "supatype_admin"}

// DevBypass is true when local open Studio is explicitly enabled (supatype dev docker only).
func DevBypass() bool {
	mode := strings.TrimSpace(strings.ToLower(os.Getenv("SUPATYPE_MODE")))
	if mode != "dev" {
		return false
	}
	v := strings.TrimSpace(strings.ToLower(os.Getenv("STUDIO_OPEN_DEV")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// AdminRolesFromEnv reads STUDIO_ADMIN_ROLES (comma-separated) or returns defaults.
func AdminRolesFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("STUDIO_ADMIN_ROLES"))
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
	root, err := os.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(clean)
}

// AdminRolesFromConfigFile merges adminRoles from admin-config.json when present.
func AdminRolesFromConfigFile(path string) []string {
	roles := AdminRolesFromEnv()
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
