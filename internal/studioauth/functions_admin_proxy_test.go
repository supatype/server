package studioauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
)

// The credential a request reached its upstream with is the service role's.
const serviceRoleBearer = "Bearer service-role-key"

// An editor calls the project's function named admin through the proxy as any other function, in
// every mode they may act in, and never with the service role's key: the old admin address hands
// a request to the admin API only when it carries that key.
func TestAnEditorCallsAFunctionNamedAdminWithoutTheServiceRole(t *testing.T) {
	for _, path := range functionNamedAdminPaths {
		for _, mode := range []string{"", ModeStudio, ModeSelf} {
			next, rec := proxyAs(t, "editor", mode, http.MethodPost, path)
			if !next.reached {
				t.Errorf("editor %q POST %s: refused %d, want the function", mode, path, rec.Code)
			}
			if next.auth == serviceRoleBearer {
				t.Errorf("editor %q POST %s carried the service role's key", mode, path)
			}
		}
	}
	if next, rec := proxyAs(t, "viewer", "", http.MethodPost, "/functions/v1/admin/orders/42"); next.reached {
		t.Errorf("a viewer wrote to a function: %d", rec.Code)
	}
}

// The admin API's own routes at its new address stay administration, whatever mode is asked for.
func TestAnEditorIsRefusedFunctionAdministration(t *testing.T) {
	for _, route := range functionAdminRoutes {
		for _, mode := range []string{"", ModeStudio, ModeSelf, ModeElevated, ModeAnon} {
			next, rec := proxyAs(t, "editor", mode, route.method, "/admin/v1/functions"+route.path)
			if next.reached || rec.Code != http.StatusForbidden {
				t.Errorf("editor %q %s %s: got %d, reached %v", mode, route.method, route.path, rec.Code, next.reached)
			}
		}
	}
}

// At the old address the gateway hands a request to the admin API only when it carries the service
// role's key, which the proxy never attaches for a caller who cannot elevate. So the gateway runs
// the function named admin for an editor's GET /functions/v1/admin/env, and the proxy judges it as
// that function call: content, under the editor's own credential.
func TestAnEditorAtTheOldAdminAddressCallsTheFunction(t *testing.T) {
	for _, route := range functionAdminRoutes {
		for _, mode := range []string{"", ModeStudio, ModeSelf, ModeAnon} {
			next, rec := proxyAs(t, "editor", mode, route.method, "/functions/v1/admin"+route.path)
			if !next.reached || next.auth == serviceRoleBearer {
				t.Errorf("editor %q %s %s: got %d with %q, want the function without the service role", mode, route.method, route.path, rec.Code, next.auth)
			}
		}
	}
	if next, rec := proxyAs(t, "viewer", "", http.MethodPost, "/functions/v1/admin/env"); next.reached {
		t.Errorf("a viewer wrote to a function: %d", rec.Code)
	}
}

// An admin acting anonymously sends the anon key, so the gateway runs the function there too.
func TestAnAdminActingAnonymouslyCallsTheFunction(t *testing.T) {
	next, rec := proxyAs(t, RoleAdmin, ModeAnon, http.MethodGet, "/functions/v1/admin/list")
	if !next.reached || next.auth != "Bearer anon-key" {
		t.Fatalf("got %d with %q, want the function with the anon key", rec.Code, next.auth)
	}
}

// Without a functions directory the gateway does not mount the admin API, and /functions/v1/admin
// is only ever the function named admin: an admin acting as themselves calls it with their own
// token, and an editor calls it as content.
func TestWithoutTheAdminAPIMountedTheOldAddressIsTheFunction(t *testing.T) {
	call := func(role, mode string) (*proxyUpstream, *httptest.ResponseRecorder) {
		req := studioRequest(http.MethodGet, "/functions/v1/admin/env", bearer(t, jwt.MapClaims{"sub": "user-1"}))
		req.Header.Set(ActingModeHeader, mode)
		cfg := membershipConfig(role)
		cfg.FunctionsAdminMounted = false
		next, rec := &proxyUpstream{}, httptest.NewRecorder()
		ProxyHandler(next, cfg).ServeHTTP(rec, req)
		return next, rec
	}
	if next, rec := call(RoleAdmin, ModeSelf); !next.reached || next.auth == serviceRoleBearer || rec.Header().Get(ActingModeHeader) != ModeSelf {
		t.Errorf("admin acting as themselves: got %d with %q, mode %q", rec.Code, next.auth, rec.Header().Get(ActingModeHeader))
	}
	if next, rec := call("editor", ModeStudio); !next.reached || next.auth == serviceRoleBearer {
		t.Errorf("editor: got %d with %q", rec.Code, next.auth)
	}
}

// Administer reaches the admin API at both addresses with the service role, acting as themselves
// or not, while a call to the function named admin acting as themselves carries their own token.
func TestAnAdminAdministersFunctionsAtBothAddresses(t *testing.T) {
	for _, address := range functionAdminAddresses {
		for _, route := range functionAdminRoutes {
			for _, mode := range []string{"", ModeSelf} {
				next, rec := proxyAs(t, RoleAdmin, mode, route.method, address+route.path)
				if !next.reached || next.auth != serviceRoleBearer {
					t.Errorf("admin %q %s %s: got %d with %q", mode, route.method, address+route.path, rec.Code, next.auth)
				}
			}
		}
	}
	if next, _ := proxyAs(t, RoleAdmin, ModeSelf, http.MethodPost, "/functions/v1/admin/orders/42"); next.auth == serviceRoleBearer {
		t.Error("an admin acting as themselves called a function with the service role's key")
	}
}

// An acting mode the proxy does not know is refused at the old address as it is on every route
// that acts in the caller's mode, rather than counted as elevating and sent as the service role.
func TestAnUnknownActingModeIsRefusedAtTheOldAdminAddress(t *testing.T) {
	for _, path := range []string{"/functions/v1/admin/list", "/functions/v1/admin/orders/42"} {
		next, rec := proxyAs(t, RoleAdmin, "bogus", http.MethodGet, path)
		if next.reached || rec.Code != http.StatusForbidden {
			t.Errorf("GET %s: got %d with %q, want 403 and no upstream", path, rec.Code, next.auth)
		}
	}
}
