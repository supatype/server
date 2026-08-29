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
	for _, role := range []string{"", "superuser", "Admin", "authenticated", "viewer"} {
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
// expectations mirror STUDIO_ROLE_PERMISSIONS in the control plane and the role
// matrix in the plan.
func TestRolePermissionsMatchTheControlPlane(t *testing.T) {
	cases := map[string]StudioPermissions{
		"admin": {
			Read: true, Write: true, SchemaView: true,
			SQLEditor: true, ElevatedSQL: true, ManageMembers: true,
		},
		"developer": {Read: true, Write: true, SchemaView: true, SQLEditor: true},
		"editor":    {Read: true, Write: true},
	}
	if len(cases) != len(KnownStudioRoles()) {
		t.Fatalf("role catalogue changed: %v", KnownStudioRoles())
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

// Only admin may hand out access. A developer who could promote themselves would
// make the whole distinction cosmetic.
func TestOnlyAdminManagesMembership(t *testing.T) {
	for _, role := range KnownStudioRoles() {
		perms, _ := PermissionsForRole(role)
		if perms.ManageMembers != (role == RoleAdmin) {
			t.Fatalf("role %q: ManageMembers=%v", role, perms.ManageMembers)
		}
		if perms.ElevatedSQL != (role == RoleAdmin) {
			t.Fatalf("role %q: ElevatedSQL=%v", role, perms.ElevatedSQL)
		}
	}
}

func TestAllowsRequest(t *testing.T) {
	editor, _ := PermissionsForRole("editor")
	developer, _ := PermissionsForRole("developer")
	admin, _ := PermissionsForRole(RoleAdmin)

	// Every role edits content rows — that is the point of the editor role.
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch} {
		if !AllowsRequest(editor, method, "/rest/v1/posts") {
			t.Fatalf("editor must be able to %s content rows", method)
		}
	}

	// The SQL runner still connects with the project's admin DSN, so it is
	// elevated regardless of who asked — admin-only until it drops to the acting
	// user's role.
	if AllowsRequest(developer, http.MethodPost, "/sql") {
		t.Fatal("developer must not reach the elevated SQL runner")
	}
	if !AllowsRequest(admin, http.MethodPost, "/sql") {
		t.Fatal("admin must reach the SQL runner")
	}

	// Membership is admin-only to change; a developer may read the list.
	if AllowsRequest(developer, http.MethodPatch, "/admin/studio-members/abc") {
		t.Fatal("developer must not change membership")
	}
	if !AllowsRequest(developer, http.MethodGet, "/admin/studio-members") {
		t.Fatal("developer should be able to read the membership list")
	}
	if AllowsRequest(editor, http.MethodGet, "/admin/studio-members") {
		t.Fatal("editor must not enumerate membership")
	}

	// User administration is a separate capability from writing content rows.
	if AllowsRequest(editor, http.MethodGet, "/auth/v1/admin/users") {
		t.Fatal("editor must not administer users")
	}
	if !AllowsRequest(admin, http.MethodGet, "/auth/v1/admin/users") {
		t.Fatal("admin must administer users")
	}
}

// Regression: the verify response hardcoded every permission to true, so an
// `editor` row was handed a full-access UI while the control plane restricted the
// same role.
func TestVerifyHandlerReportsRolePermissions(t *testing.T) {

	token := signClaims(jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	cfg := Config{
		JWTSecret:  testSecret,
		AdminRoles: DefaultAdminRoles,
		StudioRole: func(string) (string, bool) { return "editor", true },
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
	if body.Role != "editor" {
		t.Fatalf("expected role editor, got %q", body.Role)
	}
	want, _ := PermissionsForRole("editor")
	if body.Permissions != want {
		t.Fatalf("editor got the wrong capability set: %+v", body.Permissions)
	}
	if body.Permissions.ElevatedSQL || body.Permissions.ManageMembers {
		t.Fatal("editor must not be handed elevated SQL or membership management")
	}
}

func TestVerifyHandlerDeniesUnknownRole(t *testing.T) {

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
// enough to reach everything behind it.
func TestProxyHandlerEnforcesRoleOnWrites(t *testing.T) {

	token := signClaims(jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	cfg := Config{
		JWTSecret:      testSecret,
		ServiceRoleKey: "service-role-key",
		AdminRoles:     DefaultAdminRoles,
		StudioRole:     func(string) (string, bool) { return "editor", true },
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

	// An editor edits content rows, which is the role's whole purpose.
	if code := call(http.MethodPost, "/rest/v1/posts"); code != http.StatusOK || !reached {
		t.Fatalf("editor write should pass through, got %d (reached=%v)", code, reached)
	}

	// But not the elevated SQL runner or user administration.
	if code := call(http.MethodPost, "/sql"); code != http.StatusForbidden {
		t.Fatalf("editor must not reach the SQL runner, got %d", code)
	}
	if reached {
		t.Fatal("a refused request must not reach the service handler")
	}
	if code := call(http.MethodGet, "/auth/v1/admin/users"); code != http.StatusForbidden {
		t.Fatalf("editor must not administer users, got %d", code)
	}
}
