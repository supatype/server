package studioauth

import (
	"net/http"

	"github.com/supatype/server/internal/functions"
)

// legacyFunctionsAdminPrefix is the functions admin API's old address, kept for a transition, and
// also where a project function named admin is called.
const legacyFunctionsAdminPrefix = "/functions/v1/admin"

// servesFunctionsAdmin reports whether the gateway will hand this proxied request to the functions
// admin API at its old address rather than to the function named admin. It asks the gateway's own
// question (functions.ServesAdminAPI) about the request the proxy will forward: whether the API is
// mounted, and whether the proxy will attach the service role's key.
func servesFunctionsAdmin(req *http.Request, c Config, perms StudioPermissions, path string) bool {
	if !under(path, legacyFunctionsAdminPrefix) {
		return false
	}
	below := path[len(legacyFunctionsAdminPrefix):]
	return functions.ServesAdminAPI(c.FunctionsAdminMounted, elevatesAdministration(req, perms), req.Method, below)
}

// elevatesAdministration reports whether the proxy sends a request to a route that admits only the
// service role as the service role: for a caller who may elevate, in every mode but anonymous
// (admitActingMode).
func elevatesAdministration(req *http.Request, perms StudioPermissions) bool {
	mode, _ := resolveActingMode(req, perms)
	return perms.ElevatedSQL && mode != ModeAnon
}

// allowsProxied is AllowsRequest for the request the gateway will serve. By its path a request to
// the old address is a call to the function named admin; the admin API serving it instead is
// administration.
func allowsProxied(perms StudioPermissions, method, path string, functionsAdmin bool) bool {
	if functionsAdmin {
		return perms.Administer
	}
	return AllowsRequest(perms, method, path)
}
