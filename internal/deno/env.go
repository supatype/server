package deno

import (
	"os"
	"strings"

	"github.com/supatype/server/internal/config"
)

const edgeEnvPrefix = "SUPATYPE_EDGE_"

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
// Additionally, any process env key starting with SUPATYPE_EDGE_ is passed with that
// prefix stripped (e.g. SUPATYPE_EDGE_FOO=1 becomes FOO=1). The functions worker the CLI names as
// the serve script deletes every name off its own allowlist before it imports a function, so
// there these reach the worker and not the function; a function's values belong in ctx.env.
func EdgeSubprocessEnv(srv *config.Config, apiExternalURLFallback string) []string {
	if srv == nil {
		return nil
	}
	var out []string

	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, edgeEnvPrefix) {
			continue
		}
		rest := e[len(edgeEnvPrefix):]
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			continue
		}
		out = append(out, rest[:eq]+"="+rest[eq+1:])
	}

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
