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
	return locallyAddressed(c.PublicURLs)
}

// locallyAddressed reports whether every public URL this deployment knows about
// is a local address. Unset is treated as local so `supatype dev` keeps working
// before any URL is configured.
//
// The URLs are passed in rather than read from the environment: this decides
// whether an unauthenticated Studio that injects the service role key is
// allowed, so what it reads should be visible to whoever wires it up.
func locallyAddressed(urls []string) bool {
	for _, raw := range urls {
		raw = strings.TrimSpace(strings.ToLower(raw))
		if raw == "" {
			continue
		}
		switch host := hostOf(raw); {
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

// hostOf takes the host out of a configured URL.
//
// IPv6 needs handling of its own. Scanning for the first colon cut inside the
// brackets of `http://[::1]:9999` and left "[" as the host, so loopback read as
// a public address and the dev bypass refused to open Studio on it. A bare
// `::1` was cut to nothing, with the same result.
func hostOf(raw string) string {
	host := raw
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}

	switch {
	case strings.HasPrefix(host, "["):
		// The port, when there is one, follows the closing bracket.
		if end := strings.Index(host, "]"); end >= 0 {
			return host[:end+1]
		}
	case strings.Count(host, ":") > 1:
		// A bare IPv6 address cannot carry a port without brackets, so all of
		// it is the host.
		return host
	default:
		if i := strings.Index(host, ":"); i >= 0 {
			return host[:i]
		}
	}
	return host
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
