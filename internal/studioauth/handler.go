package studioauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/supatype/server/internal/serverconf"
)

// Config holds studio auth handler dependencies.
type Config struct {
	JWTSecret     string
	ServiceRoleKey string
	AdminRoles    []string
	Mode          string
	// StudioRole resolves a verified user id to their Studio role from
	// `_supatype.studio_members`. Studio capability deliberately does not come
	// from a JWT claim: `app_metadata` is the developer's namespace for their own
	// app roles, so reading it here means assigning an app role can hand out admin
	// UI access by accident. Nil keeps the legacy claim-based path, so a
	// deployment that has not been migrated still works.
	StudioRole StudioRoleLookup
}

// StudioRoleLookup returns the Studio role recorded for a user id. The second
// result is false when the user has no membership row.
type StudioRoleLookup func(userID string) (string, bool)

// ConfigFromServer builds handler config from ServerConfig and admin-config path.
func ConfigFromServer(cfg *serverconf.ServerConfig) Config {
	return Config{
		JWTSecret:      cfg.JWTSecret,
		ServiceRoleKey: cfg.ServiceRoleKey,
		AdminRoles:     AdminRolesFromConfigFile(cfg.AdminConfigPath),
		Mode:           cfg.Mode,
	}
}

// VerifyHandler serves GET /studio/auth/verify.
func VerifyHandler(c Config) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		if DevBypass() {
			writeJSON(w, http.StatusOK, verifyOKResponse(Result{
				Role: "dev-bypass",
				Sub:  "dev-bypass",
			}))
			return
		}

		result := ResolveAccess(req, c)
		if !result.Allowed {
			status := http.StatusForbidden
			if result.Sub == "" && result.Message == "Authentication required" {
				status = http.StatusUnauthorized
			}
			writeJSON(w, status, map[string]interface{}{
				"error":   "forbidden",
				"message": result.Message,
				"allowed": false,
			})
			return
		}

		writeJSON(w, http.StatusOK, verifyOKResponse(result))
	}
}

// verifyOKResponse reports the capability set Studio renders from.
//
// It used to hardcode every permission to true, so a `viewer` membership row was
// handed a full-access UI while the control plane restricted the same role — the
// two hosts disagreeing about what a role means.
func verifyOKResponse(result Result) map[string]interface{} {
	perms := result.Permissions
	if perms == nil {
		legacy := legacyAdminPermissions()
		perms = &legacy
	}
	return map[string]interface{}{
		"allowed":     true,
		"role":        result.Role,
		"sub":         result.Sub,
		"permissions": perms,
	}
}

// RequireAdmin wraps a handler with studio admin JWT checks (skipped when DevBypass).
func RequireAdmin(c Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if DevBypass() {
			next.ServeHTTP(w, req)
			return
		}
		result := ResolveAccess(req, c)
		if !result.Allowed {
			status := http.StatusForbidden
			if result.Message == "Authentication required" {
				status = http.StatusUnauthorized
			}
			writeJSON(w, status, map[string]string{"error": result.Message})
			return
		}
		next.ServeHTTP(w, req)
	})
}

// ProxyHandler forwards privileged API calls after admin JWT verification.
// inner must be the main service mux (without /studio routes).
func ProxyHandler(inner http.Handler, c Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !DevBypass() {
			result := ResolveAccess(req, c)
			if !result.Allowed {
				status := http.StatusForbidden
				if result.Message == "Authentication required" {
					status = http.StatusUnauthorized
				}
				writeJSON(w, status, map[string]string{"error": result.Message})
				return
			}

			// This handler injects the service role key, so the caller's own role
			// is the only thing limiting what they can do. Admission alone is not
			// enough — a `viewer` must not be able to write through it.
			if result.Permissions != nil &&
				!AllowsRequest(*result.Permissions, req.Method, req.URL.Path) {
				writeJSON(w, http.StatusForbidden, map[string]string{
					"error": "Studio role \"" + result.Role + "\" cannot perform this request",
				})
				return
			}
		}

		sr := strings.TrimSpace(c.ServiceRoleKey)
		if sr == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "service role key not configured"})
			return
		}

		req2 := req.Clone(req.Context())
		if req2.URL != nil {
			u := *req2.URL
			req2.URL = &u
		} else if req.URL != nil {
			u := *req.URL
			req2.URL = &u
		} else {
			req2.URL = &url.URL{}
		}
		path := req2.URL.Path
		if path == "" {
			path = "/"
		}
		req2.URL.Path = path
		req2.Header.Set("Authorization", "Bearer "+sr)
		req2.Header.Set("apikey", sr)

		inner.ServeHTTP(w, req2)
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
