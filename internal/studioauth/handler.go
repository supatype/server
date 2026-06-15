package studioauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/supatype/auth/internal/serverconf"
)

// Config holds studio auth handler dependencies.
type Config struct {
	JWTSecret     string
	ServiceRoleKey string
	AdminRoles    []string
	Mode          string
}

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
			writeJSON(w, http.StatusOK, verifyOKResponse("dev-bypass", "dev-bypass"))
			return
		}

		result := VerifyRequest(req, c.JWTSecret, c.AdminRoles)
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

		writeJSON(w, http.StatusOK, verifyOKResponse(result.Role, result.Sub))
	}
}

func verifyOKResponse(role, sub string) map[string]interface{} {
	return map[string]interface{}{
		"allowed": true,
		"role":    role,
		"sub":     sub,
		"permissions": map[string]bool{
			"read":           true,
			"write":          true,
			"manageUsers":    true,
			"manageSettings": true,
		},
	}
}

// RequireAdmin wraps a handler with studio admin JWT checks (skipped when DevBypass).
func RequireAdmin(c Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if DevBypass() {
			next.ServeHTTP(w, req)
			return
		}
		result := VerifyRequest(req, c.JWTSecret, c.AdminRoles)
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
			result := VerifyRequest(req, c.JWTSecret, c.AdminRoles)
			if !result.Allowed {
				status := http.StatusForbidden
				if result.Message == "Authentication required" {
					status = http.StatusUnauthorized
				}
				writeJSON(w, status, map[string]string{"error": result.Message})
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
