package studioauth

import (
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
