package gateway

import (
	"net/http"
	"strings"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/functions"
	"github.com/supatype/server/internal/proxy"
)

// functionsLogWorker is where the live log tail for a function is read from: the worker the
// invocation proxy sends that function's calls to (resolveFunctionsUpstreamURL), chosen per request
// from the manifest. A function with its own worker is tailed there, the rest on the project's
// worker, then the configured shared one. With none of them the functions run in-process and there
// is no worker to tail, which the admin API answers as it always has.
func functionsLogWorker(
	cfg *config.Config,
	manifestFor func(*http.Request) *proxy.RouteManifest,
) functions.WorkerResolver {
	return func(r *http.Request, function string) string {
		var m *proxy.RouteManifest
		if manifestFor != nil {
			m = manifestFor(r)
		}
		// inProcessDeno is false on purpose: the in-process address is not a worker with a tail.
		u, err := resolveFunctionsUpstreamURL(cfg, m, function, false)
		if err != nil {
			return ""
		}
		return strings.TrimRight(u.String(), "/")
	}
}
