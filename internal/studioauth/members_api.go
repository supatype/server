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
//	GET    /admin/studio-roles              — the role catalogue and its matrix
//	GET    /admin/studio-members            — current grants
//	PATCH  /admin/studio-members/{userId}   — set a role  {"role": "editor"}
//	DELETE /admin/studio-members/{userId}   — revoke
//
// Writes `_supatype.studio_members` and nothing else — never `app_metadata`,
// which belongs to the developer's own application roles.
//
// Every request re-resolves the caller's capability from the database. A session
// payload or cookie would let a demoted admin keep administering until it
// expired, and capability is exactly the thing that must not be cacheable.
func MembersAPI(c Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/admin")

		if strings.HasPrefix(path, "/studio-roles") {
			if req.Method != http.MethodGet {
				utilities.WriteJSON(w, http.StatusMethodNotAllowed, errorBody("method not allowed"))
				return
			}
			// The catalogue is not sensitive, but it still requires admission —
			// an unauthenticated caller has no business enumerating the model.
			if _, ok := requireMemberAdmin(w, req, c, false); !ok {
				return
			}
			utilities.WriteJSON(w, http.StatusOK, map[string]interface{}{"roles": roleCatalogue()})
			return
		}

		target := strings.TrimPrefix(strings.TrimPrefix(path, "/studio-members"), "/")

		switch req.Method {
		case http.MethodGet:
			if target != "" {
				utilities.WriteJSON(w, http.StatusNotFound, errorBody("not found"))
				return
			}
			if _, ok := requireMemberAdmin(w, req, c, false); !ok {
				return
			}
			members, err := c.Members.List(req.Context())
			if err != nil {
				utilities.WriteJSON(w, http.StatusServiceUnavailable,
					errorBody("could not read Studio membership"))
				return
			}
			utilities.WriteJSON(w, http.StatusOK, map[string]interface{}{"members": members})

		case http.MethodPatch:
			acting, ok := requireMemberAdmin(w, req, c, true)
			if !ok {
				return
			}
			if target == "" {
				utilities.WriteJSON(w, http.StatusBadRequest, errorBody("user id is required"))
				return
			}

			var body struct {
				Role string `json:"role"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				utilities.WriteJSON(w, http.StatusBadRequest, errorBody("invalid JSON body"))
				return
			}
			role := strings.TrimSpace(body.Role)
			if _, known := PermissionsForRole(role); !known {
				utilities.WriteJSON(w, http.StatusBadRequest, errorBody(
					"role must be one of: "+strings.Join(KnownStudioRoles(), ", ")))
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

		case http.MethodDelete:
			acting, ok := requireMemberAdmin(w, req, c, true)
			if !ok {
				return
			}
			if target == "" {
				utilities.WriteJSON(w, http.StatusBadRequest, errorBody("user id is required"))
				return
			}
			if err := c.Members.Revoke(req.Context(), acting, target); err != nil {
				writeMemberError(w, err)
				return
			}
			c.Members.Audit(req.Context(), acting, target, "revoke", "")
			utilities.WriteJSON(w, http.StatusOK, map[string]interface{}{"userId": target, "role": nil})

		default:
			utilities.WriteJSON(w, http.StatusMethodNotAllowed, errorBody("method not allowed"))
		}
	})
}

// requireMemberAdmin admits a caller and returns their user id.
//
// `mutating` distinguishes reading the membership list, which a role with schema
// visibility may do, from changing it, which only an admin may do.
func requireMemberAdmin(
	w http.ResponseWriter,
	req *http.Request,
	c Config,
	mutating bool,
) (string, bool) {
	if c.DevBypass() {
		return "", true
	}

	result := ResolveAccess(req, c)
	if !result.Allowed {
		status := http.StatusForbidden
		if result.Message == "Authentication required" {
			status = http.StatusUnauthorized
		}
		utilities.WriteJSON(w, status, errorBody(result.Message))
		return "", false
	}

	// No Permissions means the deployment is still on the legacy claim path,
	// where the claim only ever meant "is an admin".
	perms := legacyAdminPermissions()
	if result.Permissions != nil {
		perms = *result.Permissions
	}

	allowed := perms.ManageMembers
	if !mutating {
		allowed = perms.ManageMembers || perms.SchemaView
	}
	if !allowed {
		utilities.WriteJSON(w, http.StatusForbidden, errorBody(
			"Studio role \""+result.Role+"\" cannot manage Studio membership"))
		return "", false
	}

	return result.Sub, true
}

func writeMemberError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, studiomembers.ErrUnknownUser):
		utilities.WriteJSON(w, http.StatusNotFound, errorBody(err.Error()))
	case errors.Is(err, studiomembers.ErrLastAdmin):
		utilities.WriteJSON(w, http.StatusConflict, errorBody(err.Error()))
	default:
		// Self-change refusals and anything else the store rejected. These are
		// caller mistakes, not server faults.
		utilities.WriteJSON(w, http.StatusBadRequest, errorBody(err.Error()))
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

func errorBody(message string) map[string]interface{} {
	return map[string]interface{}{"error": "forbidden", "message": message}
}
