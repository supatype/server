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
// question (functions.ServesAdminAPI) about the request the proxy will forward, with the key the
// proxy will attach, once it knows the API is mounted at all.
func servesFunctionsAdmin(req *http.Request, c Config, perms StudioPermissions, path string) bool {
	if !c.FunctionsAdminMounted || !under(path, legacyFunctionsAdminPrefix) {
		return false
	}
	below := path[len(legacyFunctionsAdminPrefix):]
	return functions.ServesAdminAPI(elevatesAdministration(req, perms), req.Method, below)
}

// elevatesAdministration reports whether the proxy sends a request to a route that admits only the
// service role as the service role: for a caller who may elevate, in a mode they may act in other
// than anonymous (admitActingMode). A mode they may not act in elevates nothing: the request is
// then a function call, and admitActingMode refuses the mode as it does on any such route.
func elevatesAdministration(req *http.Request, perms StudioPermissions) bool {
	mode, ok := resolveActingMode(req, perms)
	return ok && perms.ElevatedSQL && mode != ModeAnon
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
