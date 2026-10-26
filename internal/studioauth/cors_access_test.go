package studioauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

// Who may read and change the allowed origins: anyone with schema-level trust reads them, and only
// the roles the shared matrix gives ManageDomains (owner and admin) change them. The Cloud control
// plane, having checked the member's role itself, comes in as the project with the service role key.
func TestCORSAccessFollowsTheRoleMatrix(t *testing.T) {
	token := signClaims(jwt.MapClaims{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()})
	for role, want := range map[string]struct{ read, write bool }{
		"admin":     {true, true},
		"developer": {true, false},
		"viewer":    {true, false},
		"editor":    {false, false},
		"unknown":   {false, false},
	} {
		cfg := Config{
			JWTSecret:      testSecret,
			ServiceRoleKey: "service-role-key",
			AdminRoles:     DefaultAdminRoles,
			StudioRole:     func(string) (string, bool) { return role, true },
		}
		for _, mutating := range []bool{false, true} {
			req := httptest.NewRequest(http.MethodGet, "/admin/cors-origins", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			got := CORSAccess(cfg)(rec, req, mutating)
			wantAllowed := want.read
			if mutating {
				wantAllowed = want.write
			}
			if got != wantAllowed {
				t.Errorf("%s mutating=%v: allowed = %v, want %v", role, mutating, got, wantAllowed)
			}
			if !got && rec.Code != http.StatusForbidden {
				t.Errorf("%s mutating=%v: refusal status = %d, want 403", role, mutating, rec.Code)
			}
		}
	}
}

func TestCORSAccessAdmitsTheServiceRoleKeyAndNothingLikeIt(t *testing.T) {
	cfg := Config{JWTSecret: testSecret, ServiceRoleKey: "service-role-key", AdminRoles: DefaultAdminRoles}
	for bearer, want := range map[string]bool{
		"service-role-key":  true,
		"service-role-key2": false,
		"":                  false,
	} {
		req := httptest.NewRequest(http.MethodPut, "/admin/cors-origins", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if got := CORSAccess(cfg)(httptest.NewRecorder(), req, true); got != want {
			t.Errorf("bearer %q: allowed = %v, want %v", bearer, got, want)
		}
	}
	// No key configured admits nobody by that route.
	req := httptest.NewRequest(http.MethodPut, "/admin/cors-origins", nil)
	req.Header.Set("Authorization", "Bearer ")
	if CORSAccess(Config{JWTSecret: testSecret})(httptest.NewRecorder(), req, true) {
		t.Error("an empty bearer must not match an unset key")
	}
}
