package gateway

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/functions"
	"github.com/supatype/server/internal/modes"
)

// buildLegacyFunctionsAdmin serves /functions/v1/admin while callers move to /admin/v1/functions.
func buildLegacyFunctionsAdmin(d *Deps) http.Handler {
	admin := functions.Handler(d.Config, d.Config.DenoFunctionsDir, d.FunctionLogs())
	// The invocation proxy exactly as /functions/v1 mounts it.
	invoke := wrappedHandler(functionsInvocationRoute(), d)
	return legacyFunctionsAdmin(d.Config.ServiceRoleKey, admin, invoke)
}

// legacyFunctionsAdmin is the functions admin API's old address, /functions/v1/admin.
//
// The admin API used to be mounted there, over the whole prefix, so a project function named
// "admin" could never be called: every request answered 401 "service role key required". The API
// now lives at /admin/v1/functions. For a transition, this still hands the API's own routes to it
// when the caller presents the service-role key, so a Studio or CLI not yet moved keeps working.
// Any other caller, and any other sub-path, is a call to the project's function named admin.
func legacyFunctionsAdmin(serviceRoleKey string, admin chi.Router, invoke http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if modes.ServiceRoleBearer(r, serviceRoleKey) && admin.Match(chi.NewRouteContext(), r.Method, routePathUnderMount(r)) {
			admin.ServeHTTP(w, r)
			return
		}
		invoke.ServeHTTP(w, r)
	})
}

// routePathUnderMount is the request's path below the mount that routed it, as chi tracks it. Its
// fallback is "/", the mount's own root, where studioauth's routedPath falls back to the request's
// whole path: that one judges a path anywhere on the router, this one only below this mount.
func routePathUnderMount(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePath != "" {
		return rctx.RoutePath
	}
	return "/"
}
