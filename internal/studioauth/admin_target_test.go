package studioauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/studiomembers"
)

const (
	adminUserID    = "aaaaaaaa-0000-4000-8000-000000000001"
	adminSessionID = "aaaaaaaa-0000-4000-8000-0000000000aa"
	adminEmail     = "owner@fresh-cms.test"
	plainUserID    = "bbbbbbbb-0000-4000-8000-000000000002"
)

// fakeTargets answers target lookups from a fixed project: one admin, known by
// id, session and email, and one user with no Studio role.
func fakeTargets(_ context.Context, kind studiomembers.TargetKind, value string) ([]string, error) {
	switch {
	case kind == studiomembers.TargetUserID && value == adminUserID,
		kind == studiomembers.TargetSessionID && value == adminSessionID,
		kind == studiomembers.TargetEmail && strings.EqualFold(value, adminEmail):
		return []string{"admin"}, nil
	}
	return nil, nil
}

// sendUserAdmin sends one auth request through the proxy as role.
func sendUserAdmin(t *testing.T, role, method, path, body string, lookup TargetRoleLookup) (*proxyUpstream, *httptest.ResponseRecorder) {
	t.Helper()
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	cfg := membershipConfig(role)
	cfg.TargetRoles = lookup
	next := &proxyUpstream{}
	rec := httptest.NewRecorder()
	ProxyHandler(next, cfg).ServeHTTP(rec, req)
	return next, rec
}

// adminAccountActions are the user administration requests that act on the
// admin's account, one per action Studio or the auth admin API offers.
var adminAccountActions = []struct{ name, method, path, body string }{
	{"ban", http.MethodPut, "/auth/v1/admin/users/" + adminUserID, `{"ban_duration":"876000h"}`},
	{"delete", http.MethodDelete, "/auth/v1/admin/users/" + adminUserID, ""},
	{"password", http.MethodPut, "/auth/v1/admin/users/" + adminUserID + "/", `{"password":"x"}`},
	{"app role", http.MethodPut, "/auth/v1/admin/users/" + strings.ToUpper(adminUserID), `{"app_metadata":{}}`},
	{"mfa reset", http.MethodDelete, "/auth/v1/admin/users/" + adminUserID + "/factors/" + plainUserID, ""},
	{"session revoke", http.MethodDelete, "/auth/v1/admin/sessions/" + adminSessionID, ""},
	{"magic link", http.MethodPost, "/auth/v1/admin/generate_link", `{"type":"magiclink","email":"` + adminEmail + `"}`},
	{"recovery link", http.MethodPost, "/auth/v1/admin/generate_link", `{"type":"recovery","EMAIL":"OWNER@fresh-cms.test"}`},
	{"invite resend", http.MethodPost, "/auth/v1/invite", `{"email":"` + adminEmail + `"}`},
	{"password reset", http.MethodPost, "/auth/v1/recover", `{"email":"` + adminEmail + `"}`},
}

// Fresh-user test B3: a developer disabled the admin's account from Studio
// Users. Every action on an admin's account is refused to them, plainly.
func TestADeveloperCannotActOnAnAdminsAccount(t *testing.T) {
	for _, a := range adminAccountActions {
		next, rec := sendUserAdmin(t, "developer", a.method, a.path, a.body, fakeTargets)
		if next.reached || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), refusedAdminTarget) {
			t.Errorf("%s: got %d %s, reached %v", a.name, rec.Code, rec.Body.String(), next.reached)
		}
	}
}

// An admin may act on another admin's account.
func TestAnAdminMayActOnAnAdminsAccount(t *testing.T) {
	for _, a := range adminAccountActions {
		if next, rec := sendUserAdmin(t, "admin", a.method, a.path, a.body, fakeTargets); !next.reached {
			t.Errorf("%s: admin refused with %d %s", a.name, rec.Code, rec.Body.String())
		}
	}
}

// A developer still administers every user who is not a Studio admin, and
// still reads the admin's account.
func TestADeveloperStillAdministersOtherUsers(t *testing.T) {
	for _, a := range []struct{ method, path, body string }{
		{http.MethodPut, "/auth/v1/admin/users/" + plainUserID, `{"ban_duration":"1h"}`},
		{http.MethodDelete, "/auth/v1/admin/users/" + plainUserID, ""},
		{http.MethodPost, "/auth/v1/invite", `{"email":"someone@example.com"}`},
		{http.MethodGet, "/auth/v1/admin/users/" + adminUserID, ""},
		{http.MethodPost, "/auth/v1/admin/users", `{"email":"new@example.com"}`},
	} {
		if next, rec := sendUserAdmin(t, "developer", a.method, a.path, a.body, fakeTargets); !next.reached {
			t.Errorf("%s %s: refused with %d %s", a.method, a.path, rec.Code, rec.Body.String())
		}
	}
}

// Whose account it is must be known: a lookup that fails refuses the change.
func TestADeveloperIsRefusedWhenTheTargetCannotBeChecked(t *testing.T) {
	failing := func(context.Context, studiomembers.TargetKind, string) ([]string, error) {
		return nil, errors.New("database down")
	}
	next, rec := sendUserAdmin(t, "developer", http.MethodPut, "/auth/v1/admin/users/"+plainUserID, `{}`, failing)
	if next.reached || rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, reached %v", rec.Code, next.reached)
	}
}

// The body the guard read still reaches the auth server whole.
func TestTheGuardPutsTheBodyBack(t *testing.T) {
	body := `{"email":"someone@example.com"}`
	var got string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := new(strings.Builder)
		_, _ = io.Copy(b, r.Body)
		got = b.String()
	})
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
	req := httptest.NewRequest(http.MethodPost, "/auth/v1/invite", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	cfg := membershipConfig("developer")
	cfg.TargetRoles = fakeTargets
	ProxyHandler(upstream, cfg).ServeHTTP(httptest.NewRecorder(), req)
	if got != body {
		t.Fatalf("upstream body %q, want %q", got, body)
	}
}
