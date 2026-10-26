package studioauth

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/supatype/server/internal/utilities"
)

// CORSAccess admits a caller to the CORS origins API. Reading needs schema-level trust, as Studio
// membership does; changing the list needs ManageDomains, which the shared role matrix gives owners
// and admins: which web origins may call the API is a deployment decision, like a custom domain.
//
// The service role key is also admitted. The Cloud control plane checks the member's role itself
// and then calls this API on the project's gateway as the project, the way it invites users.
func CORSAccess(c Config) func(w http.ResponseWriter, req *http.Request, mutating bool) bool {
	return func(w http.ResponseWriter, req *http.Request, mutating bool) bool {
		if isServiceRole(req, c) {
			return true
		}
		result := ResolveAccess(req, c)
		if !result.Allowed {
			status := http.StatusForbidden
			if result.Message == "Authentication required" {
				status = http.StatusUnauthorized
			}
			utilities.WriteJSON(w, status, errorBody(result.Message))
			return false
		}
		perms := permissionsOf(result)
		allowed := perms.ManageDomains
		if !mutating {
			allowed = perms.ManageDomains || perms.SchemaView
		}
		if !allowed {
			utilities.WriteJSON(w, http.StatusForbidden, errorBody(
				"Studio role \""+result.Role+"\" cannot change the allowed origins"))
			return false
		}
		return true
	}
}

// isServiceRole reports whether the request carries this deployment's service role key as its
// bearer token. Compared in constant time; an unset key admits nobody.
func isServiceRole(req *http.Request, c Config) bool {
	key := strings.TrimSpace(c.ServiceRoleKey)
	bearer, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if key == "" || !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(bearer)), []byte(key)) == 1
}
