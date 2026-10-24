package studioauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/supatype/server/internal/studiomembers"
	"github.com/supatype/server/internal/utilities"
)

// MembersAPI serves the Studio membership assignment API:
//
//	GET    /admin/studio-roles              the role catalogue and its matrix
//	GET    /admin/studio-members            current grants
//	PATCH  /admin/studio-members/{userId}   set a role  {"role": "editor"}
//	DELETE /admin/studio-members/{userId}   revoke
//
// Writes `_supatype.studio_members` and nothing else, never `app_metadata`,
// which belongs to the developer's own application roles.
//
// Every request re-resolves the caller's capability from the database. A session
// payload or cookie would let a demoted admin keep administering until it
// expired, and capability is exactly the thing that must not be cacheable.
func MembersAPI(c Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/admin")
		if strings.HasPrefix(path, "/studio-roles") {
			serveRoleCatalogue(w, req, c)
			return
		}
		target := strings.TrimPrefix(strings.TrimPrefix(path, "/studio-members"), "/")
		switch req.Method {
		case http.MethodGet:
			listMembers(w, req, c, target)
		case http.MethodPatch:
			setMemberRole(w, req, c, target)
		case http.MethodDelete:
			revokeMember(w, req, c, target)
		default:
			WriteErrorBody(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})
}

// serveRoleCatalogue answers GET /admin/studio-roles. The catalogue is not
// sensitive, but it still requires admission: an unauthenticated caller has no
// business enumerating the model.
func serveRoleCatalogue(w http.ResponseWriter, req *http.Request, c Config) {
	if req.Method != http.MethodGet {
		WriteErrorBody(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireMemberReader(w, req, c) {
		return
	}
	utilities.WriteJSON(w, http.StatusOK, map[string]interface{}{"roles": roleCatalogue()})
}

// listMembers answers GET /admin/studio-members with the current grants.
func listMembers(w http.ResponseWriter, req *http.Request, c Config, target string) {
	if target != "" {
		WriteErrorBody(w, http.StatusNotFound, "not found")
		return
	}
	if !requireMemberReader(w, req, c) {
		return
	}
	members, err := c.Members.List(req.Context())
	if err != nil {
		WriteErrorBody(w, http.StatusServiceUnavailable, "could not read Studio membership")
		return
	}
	utilities.WriteJSON(w, http.StatusOK, map[string]interface{}{"members": members})
}

// setMemberRole answers PATCH /admin/studio-members/{userId}.
func setMemberRole(w http.ResponseWriter, req *http.Request, c Config, target string) {
	acting, ok := requireMemberChange(w, req, c, target)
	if !ok {
		return
	}
	role, ok := requestedRole(w, req)
	if !ok {
		return
	}
	if err := c.Members.SetRole(req.Context(), acting, target, role); err != nil {
		writeMemberError(w, err)
		return
	}
	c.Members.Audit(req.Context(), acting, target, "set_role", role)
	utilities.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"userId": target, "role": role,
	})
}

// requestedRole reads the role a PATCH asks for, refusing, having written the
// response, a body that is not JSON or a role this deployment does not know.
func requestedRole(w http.ResponseWriter, req *http.Request) (string, bool) {
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		WriteErrorBody(w, http.StatusBadRequest, "invalid JSON body")
		return "", false
	}
	role := strings.TrimSpace(body.Role)
	if _, known := PermissionsForRole(role); !known {
		WriteErrorBody(w, http.StatusBadRequest,
			"role must be one of: "+strings.Join(KnownStudioRoles(), ", "))
		return "", false
	}
	return role, true
}

// revokeMember answers DELETE /admin/studio-members/{userId}.
func revokeMember(w http.ResponseWriter, req *http.Request, c Config, target string) {
	acting, ok := requireMemberChange(w, req, c, target)
	if !ok {
		return
	}
	if err := c.Members.Revoke(req.Context(), acting, target); err != nil {
		writeMemberError(w, err)
		return
	}
	c.Members.Audit(req.Context(), acting, target, studiomembers.ActionRevoke, "")
	utilities.WriteJSON(w, http.StatusOK, map[string]interface{}{"userId": target, "role": nil})
}

// requireMemberChange admits a caller to change target's membership and
// returns their user id. Nobody changes their own, admin or not (owner
// decision), and that is said before whether their role could change anyone's.
func requireMemberChange(w http.ResponseWriter, req *http.Request, c Config, target string) (string, bool) {
	result := ResolveAccess(req, c)
	if !result.Allowed {
		writeRefusal(w, result)
		return "", false
	}
	if studiomembers.SameUser(result.Sub, target) {
		WriteErrorBody(w, http.StatusForbidden, refusedOwnAccount)
		return "", false
	}
	if !permissionsOf(result).ManageMembers {
		writeCannotManageMembers(w, result)
		return "", false
	}
	if target == "" {
		WriteErrorBody(w, http.StatusBadRequest, "user id is required")
		return "", false
	}
	return result.Sub, true
}

// requireMemberReader admits a caller to read the membership list, which a role
// with schema visibility may do as well as one that manages members.
//
// No Permissions means the deployment is still on the legacy claim path, where
// the claim only ever meant "is an admin".
func requireMemberReader(w http.ResponseWriter, req *http.Request, c Config) bool {
	result := ResolveAccess(req, c)
	if !result.Allowed {
		writeRefusal(w, result)
		return false
	}
	perms := permissionsOf(result)
	if !perms.ManageMembers && !perms.SchemaView {
		writeCannotManageMembers(w, result)
		return false
	}
	return true
}

// writeCannotManageMembers answers a role that cannot manage membership.
func writeCannotManageMembers(w http.ResponseWriter, result Result) {
	WriteErrorBody(w, http.StatusForbidden,
		"Studio role \""+result.Role+"\" cannot manage Studio membership")
}

func writeMemberError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, studiomembers.ErrUnknownUser):
		WriteErrorBody(w, http.StatusNotFound, err.Error())
	case errors.Is(err, studiomembers.ErrLastAdmin):
		WriteErrorBody(w, http.StatusConflict, err.Error())
	default:
		// Self-change refusals and anything else the store rejected. These are
		// caller mistakes, not server faults.
		WriteErrorBody(w, http.StatusBadRequest, err.Error())
	}
}

func roleCatalogue() []map[string]interface{} {
	roles := KnownStudioRoles()
	out := make([]map[string]interface{}, 0, len(roles))
	for _, role := range roles {
		perms, _ := PermissionsForRole(role)
		out = append(out, map[string]interface{}{
			"role":        role,
			"permissions": perms,
		})
	}
	return out
}
