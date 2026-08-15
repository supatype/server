package studioauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-studio-auth-secret-32-chars!!"

func signClaims(claims jwt.MapClaims) string {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := t.SignedString([]byte(testSecret))
	if err != nil {
		panic(err)
	}
	return s
}

func TestVerifyBearerToken_appMetadataRole(t *testing.T) {
	token := signClaims(jwt.MapClaims{
		"sub":  "user-1",
		"role": "authenticated",
		"app_metadata": map[string]interface{}{
			"role": "admin",
		},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	result := VerifyBearerToken(token, testSecret, DefaultAdminRoles)
	if !result.Allowed {
		t.Fatalf("expected allowed, got %q", result.Message)
	}
	if result.Role != "admin" {
		t.Fatalf("expected role admin, got %q", result.Role)
	}
}

func TestVerifyBearerToken_topLevelAdminRole(t *testing.T) {
	token := signClaims(jwt.MapClaims{
		"sub":  "user-2",
		"role": "supatype_admin",
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	result := VerifyBearerToken(token, testSecret, DefaultAdminRoles)
	if !result.Allowed {
		t.Fatalf("expected allowed, got %q", result.Message)
	}
}

func TestVerifyBearerToken_nonAdminDenied(t *testing.T) {
	token := signClaims(jwt.MapClaims{
		"sub":  "user-3",
		"role": "authenticated",
		"app_metadata": map[string]interface{}{
			"role": "authenticated",
		},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	result := VerifyBearerToken(token, testSecret, DefaultAdminRoles)
	if result.Allowed {
		t.Fatal("expected denied")
	}
}

func TestDevBypass_requiresBothEnv(t *testing.T) {
	t.Setenv("SUPATYPE_MODE", "dev")
	t.Setenv("STUDIO_OPEN_DEV", "")
	if DevBypass() {
		t.Fatal("expected false without STUDIO_OPEN_DEV")
	}
	t.Setenv("STUDIO_OPEN_DEV", "1")
	if !DevBypass() {
		t.Fatal("expected true with STUDIO_OPEN_DEV=1")
	}
}

// Studio capability comes from `_supatype.studio_members`, not from claims: the
// claim path let a developer grant admin UI access by assigning an app role.
func TestResolveAccessPrefersMembershipOverClaims(t *testing.T) {
	secret := "studio-test-secret-at-least-32-characters"
	sign := func(claims jwt.MapClaims) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		s, err := tok.SignedString([]byte(secret))
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return s
	}

	newReq := func(token string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/studio/proxy/rest/v1/thing", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	}

	// A member with no admin claim at all is admitted.
	memberToken := sign(jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
	cfg := Config{
		JWTSecret:  secret,
		AdminRoles: DefaultAdminRoles,
		StudioRole: func(id string) (string, bool) {
			if id == "user-1" {
				return "editor", true
			}
			return "", false
		},
	}
	res := ResolveAccess(newReq(memberToken), cfg)
	if !res.Allowed || res.Role != "editor" {
		t.Fatalf("member should be admitted as editor, got allowed=%v role=%q msg=%q", res.Allowed, res.Role, res.Message)
	}

	// A claim-only "admin" with no membership row is refused.
	claimOnly := sign(jwt.MapClaims{
		"sub":          "user-2",
		"role":         "authenticated",
		"app_metadata": map[string]interface{}{"role": "admin"},
	})
	if res := ResolveAccess(newReq(claimOnly), cfg); res.Allowed {
		t.Fatal("an app_metadata role must not grant Studio access when membership is authoritative")
	}

	// Without a lookup configured the legacy claim path still works, so an
	// unmigrated deployment keeps functioning.
	legacy := Config{JWTSecret: secret, AdminRoles: DefaultAdminRoles}
	if res := ResolveAccess(newReq(claimOnly), legacy); !res.Allowed {
		t.Fatalf("legacy claim path should still admit an admin claim, got %q", res.Message)
	}

	// An unverifiable token is refused regardless of membership.
	bad := httptest.NewRequest(http.MethodGet, "/studio/proxy/x", nil)
	bad.Header.Set("Authorization", "Bearer not-a-real-token")
	if res := ResolveAccess(bad, cfg); res.Allowed {
		t.Fatal("an invalid token must never be admitted")
	}
}
