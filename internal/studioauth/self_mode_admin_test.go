package studioauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
)

// proxySelf sends one request through the proxy as a member of role who chose to
// act as themselves, and returns what reached the upstream.
func proxySelf(t *testing.T, role, method, path string) (*proxyUpstream, *httptest.ResponseRecorder) {
	t.Helper()
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
	req := studioRequest(method, path, token)
	req.Header.Set(ActingModeHeader, ModeSelf)
	next := &proxyUpstream{}
	rec := httptest.NewRecorder()
	ProxyHandler(next, membershipConfig(role)).ServeHTTP(rec, req)
	return next, rec
}

// Regression: an admin who chose "Act as myself" had every administration screen
// refused, because their own token went to upstreams that admit only the service
// role: GoTrue's admin API said "User not allowed", the storage server
// "Forbidden: service_role required", the control plane "service role key
// required". Acting as yourself is about whose policies filter project rows; it
// does not take away administration the caller's role holds.
func TestActingAsYourselfKeepsAdministration(t *testing.T) {
	for _, path := range []string{
		"/auth/v1/admin/users",
		"/storage/v1/bucket",
		"/functions/v1/admin/list",
		"/admin/v1/functions/list",
		"/platform/v1/projects/default/settings",
		"/admin/v1/config",
	} {
		next, rec := proxySelf(t, "admin", http.MethodGet, path)
		if !next.reached {
			t.Fatalf("%s: not forwarded: %d %s", path, rec.Code, rec.Body.String())
		}
		if next.auth != "Bearer service-role-key" {
			t.Errorf("%s: authorization = %q, want the service role", path, next.auth)
		}
		if got := rec.Header().Get(ActingModeHeader); got != ModeElevated {
			t.Errorf("%s: acting mode header = %q, want elevated", path, got)
		}
	}
}

// Content still acts as the caller: that is the whole of what the switch is for.
func TestActingAsYourselfStillAppliesToContent(t *testing.T) {
	for _, path := range []string{
		"/rest/v1/posts",
		"/graphql/v1",
		"/storage/v1/object/list/avatars",
		"/auth/v1/user",
		// A project function named admin, not the admin API at its old address.
		"/functions/v1/admin/orders/42",
	} {
		next, _ := proxySelf(t, "admin", http.MethodGet, path)
		if next.auth == "Bearer service-role-key" {
			t.Errorf("%s: elevated, want the caller's own token", path)
		}
	}
}

// A role that cannot elevate is not handed the service role by an administration
// route either: what it may reach is unchanged.
func TestAdministrationDoesNotElevateARoleThatCannot(t *testing.T) {
	next, _ := proxySelf(t, "editor", http.MethodGet, "/storage/v1/bucket")
	if next.auth == "Bearer service-role-key" {
		t.Error("an editor's bucket request was elevated")
	}
}

// Regression: Studio's client refreshes its session through the proxy, and the
// proxy refused the refresh ("Authentication required") because a refresh carries
// no access token, only the refresh token. The client then cleared the session it
// shares with the sign-in client, so the next reload (the acting-mode switch is
// one) showed the sign-in screen. The refresh grant is GoTrue's public endpoint,
// whose credential is the refresh token itself; it passes through untouched.
func TestTheProxyPassesATokenRefreshThrough(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/auth/v1/token?grant_type=refresh_token", nil)
	req.Header.Set("Authorization", "Bearer anon-key")
	req.Header.Set("apikey", "anon-key")
	req.Header.Set(StudioActorHeader, "someone-else")
	next := &proxyUpstream{}
	rec := httptest.NewRecorder()
	ProxyHandler(next, membershipConfig("admin")).ServeHTTP(rec, req)

	if !next.reached {
		t.Fatalf("the refresh was refused: %d %s", rec.Code, rec.Body.String())
	}
	if next.auth != "Bearer anon-key" || next.apikey != "anon-key" {
		t.Errorf("credentials changed: authorization %q, apikey %q", next.auth, next.apikey)
	}
}

// Only the refresh grant: a password grant, or any other auth route, is still
// admitted as before.
func TestTheProxyPassesNoOtherTokenGrant(t *testing.T) {
	for _, target := range []string{
		"/auth/v1/token?grant_type=password",
		"/auth/v1/admin/users",
	} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		next := &proxyUpstream{}
		rec := httptest.NewRecorder()
		ProxyHandler(next, membershipConfig("admin")).ServeHTTP(rec, req)
		if next.reached {
			t.Errorf("%s reached the upstream without admission", target)
		}
	}
	get := httptest.NewRequest(http.MethodGet, "/auth/v1/token?grant_type=refresh_token", nil)
	next := &proxyUpstream{}
	ProxyHandler(next, membershipConfig("admin")).ServeHTTP(httptest.NewRecorder(), get)
	if next.reached {
		t.Error("a GET to the token endpoint reached the upstream without admission")
	}
}
