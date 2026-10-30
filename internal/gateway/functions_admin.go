package gateway

import (
	"net/http"

	"github.com/supatype/server/internal/functions"
	"github.com/supatype/server/internal/modes"
	"github.com/supatype/server/internal/utilities"
)

// functionsAdminAPI is the functions admin API over this server's functions directory, as both of
// its addresses serve it.
func functionsAdminAPI(d *Deps) http.Handler {
	return functions.Handler(d.Config, d.Config.DenoFunctionsDir, d.FunctionLogs())
}

// buildLegacyFunctionsAdmin serves /functions/v1/admin while callers move to /admin/v1/functions.
func buildLegacyFunctionsAdmin(d *Deps) http.Handler {
	// The invocation proxy exactly as /functions/v1 mounts it.
	invoke := wrappedHandler(functionsInvocationRoute(), d)
	return legacyFunctionsAdmin(d.Config.ServiceRoleKey, functionsAdminAPI(d), invoke)
}

// legacyFunctionsAdmin is the functions admin API's old address, /functions/v1/admin.
//
// The admin API used to be mounted there, over the whole prefix, so a project function named
// "admin" could never be called: every request answered 401 "service role key required". The API
// now lives at /admin/v1/functions. For a transition, this still hands the API's own routes to it
// (functions.ServesAdminAPI, which the Studio proxy judges by too) when the caller presents the
// service-role key, so a Studio or CLI not yet moved keeps working. Any other caller, and any
// other sub-path, is a call to the project's function named admin. With no invocation proxy built,
// /functions/v1 is not mounted either, and those calls answer 404 as the rest of /functions/v1 does.
func legacyFunctionsAdmin(serviceRoleKey string, admin, invoke http.Handler) http.Handler {
	if invoke == nil {
		invoke = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The mount's own root when chi left no remainder: this judges a path below this mount only.
		path := utilities.RoutePath(r, "/")
		if functions.ServesAdminAPI(modes.ServiceRoleBearer(r, serviceRoleKey), r.Method, path) {
			admin.ServeHTTP(w, r)
			return
		}
		invoke.ServeHTTP(w, r)
	})
}
