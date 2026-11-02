package studioauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Admins and developers act elevated by default, with full access as before.
// Editors and viewers act as Studio: their Studio role, not the app's rules,
// decides what they may do with content (owner decision 2026-10-21).
func TestActingModeDefaults(t *testing.T) {
	for role, want := range map[string]string{
		"admin": ModeElevated, "developer": ModeElevated, "editor": ModeStudio, "viewer": ModeStudio,
	} {
		if mode, _ := modeFor(t, role, ""); mode != want {
			t.Fatalf("%s should default to %s, got %q", role, want, mode)
		}
	}
}

// Any role that reads content may ask to act as Studio, admins and developers
// included.
func TestAnyReaderMayActAsStudio(t *testing.T) {
	for _, role := range KnownStudioRoles() {
		if mode, ok := modeFor(t, role, ModeStudio); !ok || mode != ModeStudio {
			t.Fatalf("%s should be able to act as studio, got (%q, %v)", role, mode, ok)
		}
	}
}

// Dropping privilege needs no permission. That is what makes the P3 toggle
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
// downgraded: a misleadingly empty result is worse than an error. Admins and
// developers hold elevated SQL; editors and viewers do not.
func TestElevationRequiresTheCapability(t *testing.T) {
	for _, role := range []string{"editor", "viewer"} {
		if _, ok := modeFor(t, role, ModeElevated); ok {
			t.Fatalf("%s must not be able to elevate", role)
		}
	}
	for _, role := range []string{"admin", "developer"} {
		if mode, ok := modeFor(t, role, ModeElevated); !ok || mode != ModeElevated {
			t.Fatalf("%s should be able to elevate, got (%q, %v)", role, mode, ok)
		}
	}
}

func TestUnknownActingModeIsRefused(t *testing.T) {
	if _, ok := modeFor(t, "admin", "root"); ok {
		t.Fatal("an unrecognised acting mode must be refused")
	}
}

// An acting mode the caller cannot act in is refused on every route, with the same 403 a data
// route gives, before anything decides to elevate. The routes that admit only the service role
// used to elevate a caller who may, whatever they asked for, so a mode the proxy does not know
// was forwarded with the service role's key.
func TestUnknownActingModeIsRefusedOnEveryRoute(t *testing.T) {
	const refusal = "cannot act in the mode it asked for"
	for _, path := range []string{
		"/admin/v1/config",
		"/admin/v1/functions/list",
		"/auth/v1/admin/users",
		"/storage/v1/bucket",
		"/platform/v1/projects/default/settings",
		"/functions/v1/admin/list",
		"/sql",
		"/metadata/introspect",
		"/rest/v1/posts",
	} {
		next, rec := proxyAs(t, RoleAdmin, "bogus", http.MethodGet, path)
		if next.reached || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), refusal) {
			t.Errorf("GET %s: got %d (%s) with %q, want the invalid-mode 403", path, rec.Code, rec.Body.String(), next.auth)
		}
	}
}

// Regression: every Studio request was forwarded with the service role key, so
// admission to the panel was the same thing as unrestricted database access.
// Content rows now go as Studio (the caller's own identity with the Studio
// claim), as the caller themselves when they ask, and as the service role only
// for a role that may elevate and asks to.
func TestProxyForwardsCallerTokenForNonElevatedRoles(t *testing.T) {
	const serviceRole = "service-role-key"
	token := signClaims(jwt.MapClaims{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()})

	var seenAuth, seenKey string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenAuth, seenKey = r.Header.Get("Authorization"), r.Header.Get("apikey")
	})
	call := func(role, requested string) *httptest.ResponseRecorder {
		cfg := membershipConfig(role)
		seenAuth, seenKey = "", ""
		req := studioRequest(http.MethodGet, "/rest/v1/posts", token)
		if requested != "" {
			req.Header.Set(ActingModeHeader, requested)
		}
		rec := httptest.NewRecorder()
		ProxyHandler(inner, cfg).ServeHTTP(rec, req)
		return rec
	}

	for _, role := range KnownStudioRoles() {
		rec := call(role, ModeStudio)
		if claims := studioClaims(t, seenAuth); claims["sub"] != "user-1" || seenKey == serviceRole {
			t.Fatalf("%s: rows must go as the caller with the Studio claim, got %v (apikey %q)", role, claims, seenKey)
		}
		if got := rec.Header().Get(ActingModeHeader); got != ModeStudio {
			t.Fatalf("%s: expected the response to report studio, got %q", role, got)
		}
		if rec = call(role, ModeSelf); seenAuth != "Bearer "+token || rec.Header().Get(ActingModeHeader) != ModeSelf {
			t.Fatalf("%s acting as self should forward their own token, got %q", role, seenAuth)
		}
	}

	// By default editors and viewers go as Studio, admins and developers elevated.
	for _, role := range []string{"editor", "viewer"} {
		if rec := call(role, ""); rec.Header().Get(ActingModeHeader) != ModeStudio {
			t.Fatalf("%s by default: mode %q", role, rec.Header().Get(ActingModeHeader))
		}
		studioClaims(t, seenAuth)
	}
	for _, role := range []string{"admin", "developer"} {
		if rec := call(role, ""); seenAuth != "Bearer "+serviceRole || rec.Header().Get(ActingModeHeader) != ModeElevated {
			t.Fatalf("%s by default gets the service role, got %q", role, seenAuth)
		}
	}
	// An editor asking to elevate is refused, not quietly downgraded.
	if rec := call("editor", ModeElevated); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestVerifyReportsActingMode(t *testing.T) {

	token := signClaims(jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	check := func(role, wantMode string, wantCanElevate bool) {
		cfg := Config{
			Audit:      &fakeAuditor{},
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
	check("developer", ModeElevated, true)
	check("editor", ModeStudio, false)
	check("viewer", ModeStudio, false)
}

// Verify reports the mode the caller's requests will act in. A mode the caller
// may not have, or one that does not exist, is reported as the caller's
// default rather than echoed back, so Studio's switch never shows a mode its
// requests will not get.
func TestVerifyReportsTheDefaultForAModeTheCallerCannotHave(t *testing.T) {
	token := signClaims(jwt.MapClaims{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()})
	for _, tc := range []struct{ role, asked, want string }{
		{"admin", "root", ModeElevated},
		{"admin", ModeMediated, ModeElevated},
		{"editor", ModeElevated, ModeStudio},
		{"viewer", "root", ModeStudio},
		{"admin", ModeSelf, ModeSelf},
		{"admin", ModeStudio, ModeStudio},
		{"developer", ModeStudio, ModeStudio},
		{"developer", ModeElevated, ModeElevated},
	} {
		role := tc.role
		cfg := Config{
			Audit:      &fakeAuditor{},
			JWTSecret:  testSecret,
			AdminRoles: DefaultAdminRoles,
			StudioRole: func(string) (string, bool) { return role, true },
		}
		req := httptest.NewRequest(http.MethodGet, "/studio/auth/verify", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(ActingModeHeader, tc.asked)
		rec := httptest.NewRecorder()
		VerifyHandler(cfg).ServeHTTP(rec, req)

		var body struct {
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Mode != tc.want {
			t.Errorf("%s asking for %q: reported %q, want %q", tc.role, tc.asked, body.Mode, tc.want)
		}
	}
}

// A capability set that neither elevates nor reads content has nothing to act
// with but the caller's own identity, so that is its default.
func TestACapabilitySetThatReadsNothingActsAsSelf(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	if mode, ok := resolveActingMode(req, StudioPermissions{SchemaView: true}); !ok || mode != ModeSelf {
		t.Fatalf("got (%q, %v), want self", mode, ok)
	}
}
