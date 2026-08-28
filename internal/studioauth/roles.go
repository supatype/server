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
//
// Mode and OpenDev come from configuration rather than the environment. The
// locality check below still reads the deployment's public URL directly; those
// are auth-service variables and move with the rename.
func (c Config) DevBypass() bool {
	if strings.TrimSpace(strings.ToLower(c.Mode)) != "dev" {
		return false
	}
	if !c.OpenDev {
		return false
	}
	// An open Studio proxy answers unauthenticated requests *and* injects the
	// service role key, so it is full database access. Two env vars are easy to
	// carry from a laptop to a server by copying a config, so also require the
	// deployment to be locally addressed: convenience stays on localhost and
	// cannot follow the config into production.
	return isLocallyAddressed()
}

// isLocallyAddressed reports whether this deployment's public URL is a local
// address. Unset is treated as local so `supatype dev` keeps working before any
// URL is configured.
func isLocallyAddressed() bool {
	for _, key := range []string{"API_EXTERNAL_URL", "GOTRUE_API_EXTERNAL_URL", "GOTRUE_SITE_URL"} {
		raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
		if raw == "" {
			continue
		}
		host := raw
		if i := strings.Index(host, "://"); i >= 0 {
			host = host[i+3:]
		}
		if i := strings.IndexAny(host, "/:"); i >= 0 {
			host = host[:i]
		}
		switch {
		case host == "localhost", host == "127.0.0.1", host == "[::1]", host == "::1":
		case strings.HasSuffix(host, ".localhost"), strings.HasSuffix(host, ".local"):
		case host == "lvh.me", strings.HasSuffix(host, ".lvh.me"):
		case host == "host.docker.internal":
		default:
			return false
		}
	}
	return true
}

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
	root, err := os.OpenRoot(".")
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
