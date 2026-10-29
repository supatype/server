package studioauth

import (
	"net/http"
	"testing"
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

// The admin API's own routes stay administration at both addresses, whatever mode is asked for.
func TestAnEditorIsRefusedFunctionAdministration(t *testing.T) {
	for _, address := range functionAdminAddresses {
		for _, route := range functionAdminRoutes {
			for _, mode := range []string{"", ModeStudio, ModeSelf, ModeElevated, ModeAnon} {
				next, rec := proxyAs(t, "editor", mode, route.method, address+route.path)
				if next.reached || rec.Code != http.StatusForbidden {
					t.Errorf("editor %q %s %s: got %d, reached %v", mode, route.method, address+route.path, rec.Code, next.reached)
				}
			}
		}
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
