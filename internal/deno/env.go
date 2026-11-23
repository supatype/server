package deno

import (
	"strings"

	"github.com/supatype/server/internal/config"
)

// EdgeSubprocessEnv returns KEY=value pairs appended after the allowlisted part of os.Environ()
// for the Deno child (allowlist.go). Go merges env with last-wins for duplicate keys, so these
// override inherited values, and they are not filtered.
//
// Injected when non-empty:
//   - SUPATYPE_URL: from srv.SupatypeURL, else apiExternalURLFallback
//   - SUPATYPE_ANON_KEY: from srv.AnonKey
//   - SUPATYPE_SERVICE_ROLE_KEY: from srv.ServiceRoleKey, which the worker withholds from every
//     route not granted it
//
// Nothing else is read from the process environment: a function's own values reach it in ctx.env
// only, and a SUPATYPE_EDGE_ prefix has no meaning.
func EdgeSubprocessEnv(srv *config.Config, apiExternalURLFallback string) []string {
	if srv == nil {
		return nil
	}
	var out []string

	url := strings.TrimSpace(srv.SupatypeURL)
	if url == "" {
		url = strings.TrimSpace(apiExternalURLFallback)
	}
	if url != "" {
		out = append(out, "SUPATYPE_URL="+url)
	}
	if k := strings.TrimSpace(srv.AnonKey); k != "" {
		out = append(out, "SUPATYPE_ANON_KEY="+k)
	}
	if k := strings.TrimSpace(srv.ServiceRoleKey); k != "" {
		out = append(out, "SUPATYPE_SERVICE_ROLE_KEY="+k)
	}
	return out
}
