package studioauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

func TestPermissionsForRoleFailsClosed(t *testing.T) {
	for _, role := range []string{"", "superuser", "Admin", "authenticated"} {
		if _, ok := PermissionsForRole(role); ok {
			t.Fatalf("role %q must not be recognised", role)
		}
	}
	for _, role := range KnownStudioRoles() {
		if _, ok := PermissionsForRole(role); !ok {
			t.Fatalf("role %q should be recognised", role)
		}
	}
}

// The same membership row must mean the same thing in both hosts; these
// expectations mirror STUDIO_ROLE_PERMISSIONS in the control plane.
func TestRolePermissionsMatchTheControlPlane(t *testing.T) {
	cases := map[string]StudioPermissions{
		"admin":  {Read: true, Write: true, ManageUsers: true, ManageSettings: true},
		"editor": {Read: true, Write: true},
		"viewer": {Read: true},
	}
	for role, want := range cases {
		got, ok := PermissionsForRole(role)
		if !ok {
			t.Fatalf("role %q missing", role)
		}
		if got != want {
			t.Fatalf("role %q: got %+v, want %+v", role, got, want)
		}
	}
}

func TestAllowsRequest(t *testing.T) {
	viewer, _ := PermissionsForRole("viewer")
	editor, _ := PermissionsForRole("editor")
	admin, _ := PermissionsForRole("admin")

	// A reader may read rows and nothing else.
	if !AllowsRequest(viewer, http.MethodGet, "/rest/v1/posts") {
		t.Fatal("viewer must be able to read rows")
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		if AllowsRequest(viewer, method, "/rest/v1/posts") {
			t.Fatalf("viewer must not be able to %s rows", method)
		}
	}

	// The SQL runner is unrestricted database access, so it is admin-only even
	// for a role that can otherwise write.
	if AllowsRequest(editor, http.MethodPost, "/sql") {
		t.Fatal("editor must not reach the SQL runner")
	}
	if !AllowsRequest(admin, http.MethodPost, "/sql") {
		t.Fatal("admin must reach the SQL runner")
	}

	// User administration is a separate capability from writing rows.
	if AllowsRequest(editor, http.MethodGet, "/auth/v1/admin/users") {
		t.Fatal("editor must not administer users")
	}
	if !AllowsRequest(admin, http.MethodGet, "/auth/v1/admin/users") {
		t.Fatal("admin must administer users")
	}
}

// Regression: the verify response hardcoded every permission to true, so a
// `viewer` row was handed a full-access UI while the control plane restricted the
// same role.
func TestVerifyHandlerReportsRolePermissions(t *testing.T) {
	t.Setenv("SUPATYPE_MODE", "")
	t.Setenv("STUDIO_OPEN_DEV", "")

	token := signClaims(jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	cfg := Config{
		JWTSecret:  testSecret,
		AdminRoles: DefaultAdminRoles,
		StudioRole: func(string) (string, bool) { return "viewer", true },
	}

	req := httptest.NewRequest(http.MethodGet, "/studio/auth/verify", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	VerifyHandler(cfg).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Role        string            `json:"role"`
		Permissions StudioPermissions `json:"permissions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Role != "viewer" {
		t.Fatalf("expected role viewer, got %q", body.Role)
	}
	if !body.Permissions.Read || body.Permissions.Write ||
		body.Permissions.ManageUsers || body.Permissions.ManageSettings {
		t.Fatalf("viewer got the wrong capability set: %+v", body.Permissions)
	}
}

func TestVerifyHandlerDeniesUnknownRole(t *testing.T) {
	t.Setenv("SUPATYPE_MODE", "")
	t.Setenv("STUDIO_OPEN_DEV", "")

	token := signClaims(jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	cfg := Config{
		JWTSecret:  testSecret,
		AdminRoles: DefaultAdminRoles,
		StudioRole: func(string) (string, bool) { return "superuser", true },
	}

	req := httptest.NewRequest(http.MethodGet, "/studio/auth/verify", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	VerifyHandler(cfg).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for an unrecognised role, got %d", rec.Code)
	}
}

// The Studio proxy injects the service role key, so admission alone must not be
// enough to write.
func TestProxyHandlerEnforcesRoleOnWrites(t *testing.T) {
	t.Setenv("SUPATYPE_MODE", "")
	t.Setenv("STUDIO_OPEN_DEV", "")

	token := signClaims(jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	cfg := Config{
		JWTSecret:      testSecret,
		ServiceRoleKey: "service-role-key",
		AdminRoles:     DefaultAdminRoles,
		StudioRole:     func(string) (string, bool) { return "viewer", true },
	}

	reached := false
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
	handler := ProxyHandler(inner, cfg)

	call := func(method, path string) int {
		reached = false
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := call(http.MethodGet, "/rest/v1/posts"); code != http.StatusOK || !reached {
		t.Fatalf("viewer read should pass through, got %d (reached=%v)", code, reached)
	}
	if code := call(http.MethodPost, "/rest/v1/posts"); code != http.StatusForbidden {
		t.Fatalf("viewer write should be refused, got %d", code)
	}
	if reached {
		t.Fatal("a refused write must not reach the service handler")
	}
}
