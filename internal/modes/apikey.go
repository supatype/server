package modes

import (
	"encoding/json"
	"net/http"
	"strings"

	jwt "github.com/golang-jwt/jwt/v5"
)

// Data-plane prefixes that require a project API key. `/auth/v1` is deliberately
// absent: the auth service enforces its own credentials there, and the control plane
// proxies admin calls through it.
var apiKeyProtectedPrefixes = []string{
	"/rest/v1",
	"/storage/v1",
	"/realtime/v1",
	"/functions/v1",
}

// apiKeyRequired reports whether this request must carry a project API key.
//
// The exemptions are not conveniences — each one is a caller that physically
// cannot send a header:
//   - CORS preflight (OPTIONS) carries no custom headers, so gating it breaks
//     every browser request;
//   - public storage objects are fetched by <img src> and plain links.
func apiKeyRequired(r *http.Request) bool {
	if r.Method == http.MethodOptions {
		return false
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/storage/v1/object/public/") {
		return false
	}
	for _, prefix := range apiKeyProtectedPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// extractAPIKey reads the key from the header, the bearer token, or the query
// string. The query string is required because a browser cannot set headers on
// a WebSocket upgrade, which is how Realtime connects.
func extractAPIKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("apikey")); key != "" {
		return key
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); auth != "" {
		if len(auth) > 7 && strings.EqualFold(auth[:7], "bearer ") {
			if token := strings.TrimSpace(auth[7:]); token != "" {
				return token
			}
		}
	}
	return strings.TrimSpace(r.URL.Query().Get("apikey"))
}

// APIKeyRoleFromToken verifies a key against the project's JWT secret and
// returns its role. An unverifiable key, or one whose role is not a PostgREST
// role, yields "".
func APIKeyRoleFromToken(token, jwtSecret string) string {
	if token == "" || strings.TrimSpace(jwtSecret) == "" {
		return ""
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(jwtSecret), nil
	}); err != nil {
		return ""
	}

	// The top-level `role` claim is the PostgREST role. Application roles live in
	// app_metadata and are irrelevant here — this gate only proves the caller
	// holds a key issued for this project.
	role, _ := claims["role"].(string)
	switch role {
	case "anon", "authenticated", "service_role":
		return role
	default:
		return ""
	}
}

// APIKeyMiddleware rejects data-plane requests that do not carry a valid project
// API key. Without it, an unkeyed request reaches PostgREST and is executed as
// the anon role, leaving the whole API surface reachable by anyone who knows the
// project ref — unattributable and impossible to rate limit or revoke.
func APIKeyMiddleware(jwtSecret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !apiKeyRequired(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Fail closed on misconfiguration rather than waving traffic through.
		if strings.TrimSpace(jwtSecret) == "" {
			writeJSONError(w, http.StatusInternalServerError, "misconfigured",
				"API key verification is not configured on this gateway")
			return
		}

		key := extractAPIKey(r)
		if key == "" {
			writeJSONError(w, http.StatusUnauthorized, "no_api_key",
				"No API key found in request. Pass the project anon key as the `apikey` header.")
			return
		}
		if APIKeyRoleFromToken(key, jwtSecret) == "" {
			writeJSONError(w, http.StatusUnauthorized, "invalid_api_key",
				"Invalid API key for this project.")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeJSONError mirrors the shape Kong and the control plane return, so clients
// can handle gateway errors uniformly.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
