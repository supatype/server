package studioauth

import (
	"net/http"
	"slices"
	"strings"

	jwt "github.com/golang-jwt/jwt/v5"
)

// Result is the outcome of an admin JWT check.
type Result struct {
	Allowed bool
	Message string
	Role    string
	Sub     string
}

// VerifyBearerToken validates a user JWT and checks studio admin role membership.
// Role resolution: app_metadata.role first, then top-level role when it is an admin role.
func VerifyBearerToken(token string, jwtSecret string, adminRoles []string) Result {
	token = strings.TrimSpace(token)
	if token == "" {
		return Result{Allowed: false, Message: "Authentication required"}
	}
	if strings.TrimSpace(jwtSecret) == "" {
		return Result{Allowed: false, Message: "JWT secret not configured"}
	}

	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(jwtSecret), nil
	})
	if err != nil {
		return Result{Allowed: false, Message: "Invalid or expired token"}
	}

	sub, _ := claims["sub"].(string)
	if strings.TrimSpace(sub) == "" {
		return Result{Allowed: false, Message: "Token missing subject claim"}
	}

	role, ok := resolveStudioAdminRole(claims, adminRoles)
	if !ok {
		return Result{
			Allowed: false,
			Message: "You don't have permission to access the admin panel",
			Sub:     sub,
			Role:    role,
		}
	}

	return Result{Allowed: true, Message: "ok", Role: role, Sub: sub}
}

// VerifyRequest extracts Bearer token from req and verifies it.
func VerifyRequest(req *http.Request, jwtSecret string, adminRoles []string) Result {
	return VerifyBearerToken(extractBearerToken(req), jwtSecret, adminRoles)
}

func resolveStudioAdminRole(claims jwt.MapClaims, adminRoles []string) (string, bool) {
	if appMeta, ok := claims["app_metadata"].(map[string]interface{}); ok {
		if r, ok := appMeta["role"].(string); ok && r != "" {
			if slices.Contains(adminRoles, r) {
				return r, true
			}
		}
	}
	if r, ok := claims["role"].(string); ok && r != "" {
		if slices.Contains(adminRoles, r) {
			return r, true
		}
	}
	// Expose resolved non-admin role for error messages when present.
	if appMeta, ok := claims["app_metadata"].(map[string]interface{}); ok {
		if r, ok := appMeta["role"].(string); ok && r != "" {
			return r, false
		}
	}
	if r, ok := claims["role"].(string); ok && r != "" {
		return r, false
	}
	return "", false
}

func extractBearerToken(req *http.Request) string {
	auth := strings.TrimSpace(req.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}
