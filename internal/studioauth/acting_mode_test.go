package studioauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

func modeFor(t *testing.T, role, requested string) (string, bool) {
	t.Helper()
	perms, ok := PermissionsForRole(role)
	if !ok {
		t.Fatalf("unknown role %q", role)
	}
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	if requested != "" {
		req.Header.Set(ActingModeHeader, requested)
	}
	return resolveActingMode(req, perms)
}

// The default keeps an administrator's panel working while confining the roles a
// project would hand to a contractor to their own RLS policies.
func TestActingModeDefaults(t *testing.T) {
	if mode, _ := modeFor(t, "admin", ""); mode != ModeElevated {
		t.Fatalf("admin should default to elevated, got %q", mode)
	}
	for _, role := range []string{"developer", "editor"} {
		if mode, _ := modeFor(t, role, ""); mode != ModeSelf {
			t.Fatalf("%s should default to self, got %q", role, mode)
		}
	}
}

// Dropping privilege needs no permission — that is what makes the P3 toggle
// possible for an admin who wants to see what their users see.
func TestAnyRoleMayActAsSelf(t *testing.T) {
	for _, role := range KnownStudioRoles() {
		mode, ok := modeFor(t, role, ModeSelf)
		if !ok || mode != ModeSelf {
			t.Fatalf("%s should be able to act as self, got (%q, %v)", role, mode, ok)
		}
	}
}

// Asking for elevation without the capability is refused rather than silently
// downgraded: a misleadingly empty result is worse than an error.
func TestElevationRequiresTheCapability(t *testing.T) {
	for _, role := range []string{"developer", "editor"} {
		if _, ok := modeFor(t, role, ModeElevated); ok {
			t.Fatalf("%s must not be able to elevate", role)
		}
	}
	if mode, ok := modeFor(t, "admin", ModeElevated); !ok || mode != ModeElevated {
		t.Fatalf("admin should be able to elevate, got (%q, %v)", mode, ok)
	}
}

func TestUnknownActingModeIsRefused(t *testing.T) {
	if _, ok := modeFor(t, "admin", "root"); ok {
		t.Fatal("an unrecognised acting mode must be refused")
	}
}

// Regression: every Studio request was forwarded with the service role key, so
// admission to the panel was the same thing as unrestricted database access.
func TestProxyForwardsCallerTokenForNonElevatedRoles(t *testing.T) {
	t.Setenv("SUPATYPE_MODE", "")
	t.Setenv("STUDIO_OPEN_DEV", "")

	const serviceRole = "service-role-key"
	token := signClaims(jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	var seenAuth, seenKey string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenKey = r.Header.Get("apikey")
	})

	call := func(role, requested string) *httptest.ResponseRecorder {
		cfg := Config{
			JWTSecret:      testSecret,
			ServiceRoleKey: serviceRole,
			AnonKey:        "anon-key",
			AdminRoles:     DefaultAdminRoles,
			StudioRole:     func(string) (string, bool) { return role, true },
		}
		seenAuth, seenKey = "", ""
		req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if requested != "" {
			req.Header.Set(ActingModeHeader, requested)
		}
		rec := httptest.NewRecorder()
		ProxyHandler(inner, cfg).ServeHTTP(rec, req)
		return rec
	}

	// A developer's request reaches PostgREST as themselves, so RLS applies.
	rec := call("developer", "")
	if seenAuth != "Bearer "+token {
		t.Fatalf("expected the caller's own token, got %q", seenAuth)
	}
	if seenKey == serviceRole {
		t.Fatal("apikey must not carry the service role for a non-elevated request")
	}
	if got := rec.Header().Get(ActingModeHeader); got != ModeSelf {
		t.Fatalf("expected the response to report self, got %q", got)
	}

	// An admin still gets the elevated path by default.
	rec = call("admin", "")
	if seenAuth != "Bearer "+serviceRole {
		t.Fatalf("expected the service role for an admin, got %q", seenAuth)
	}
	if got := rec.Header().Get(ActingModeHeader); got != ModeElevated {
		t.Fatalf("expected the response to report elevated, got %q", got)
	}

	// And an admin can choose to see what their users see.
	rec = call("admin", ModeSelf)
	if seenAuth != "Bearer "+token {
		t.Fatalf("admin acting as self should forward their own token, got %q", seenAuth)
	}
	if got := rec.Header().Get(ActingModeHeader); got != ModeSelf {
		t.Fatalf("expected self, got %q", got)
	}

	// An editor asking to elevate is refused, not quietly downgraded.
	rec = call("editor", ModeElevated)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestVerifyReportsActingMode(t *testing.T) {
	t.Setenv("SUPATYPE_MODE", "")
	t.Setenv("STUDIO_OPEN_DEV", "")

	token := signClaims(jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	check := func(role, wantMode string, wantCanElevate bool) {
		cfg := Config{
			JWTSecret:  testSecret,
			AdminRoles: DefaultAdminRoles,
			StudioRole: func(string) (string, bool) { return role, true },
		}
		req := httptest.NewRequest(http.MethodGet, "/studio/auth/verify", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		VerifyHandler(cfg).ServeHTTP(rec, req)

		var body struct {
			Mode       string `json:"mode"`
			CanElevate bool   `json:"canElevate"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Mode != wantMode || body.CanElevate != wantCanElevate {
			t.Fatalf("%s: got mode=%q canElevate=%v", role, body.Mode, body.CanElevate)
		}
	}

	check("admin", ModeElevated, true)
	check("developer", ModeSelf, false)
	check("editor", ModeSelf, false)
}
