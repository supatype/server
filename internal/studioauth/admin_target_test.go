package studioauth

import (
	"context"
	"encoding/json"
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

const (
	ownUserID    = "dddddddd-0000-4000-8000-000000000004"
	ownSessionID = "dddddddd-0000-4000-8000-0000000000dd"
)

// sendOwnAccount sends one auth request through the proxy as role, signed in as
// ownUserID with the claims a signed-in project user carries.
func sendOwnAccount(t *testing.T, role, method, path, body string) (*proxyUpstream, *httptest.ResponseRecorder) {
	t.Helper()
	token := bearer(t, jwt.MapClaims{
		"sub": ownUserID, "role": "authenticated",
		"app_metadata": map[string]interface{}{"provider": "email", "role": "member"},
	})
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	cfg := membershipConfig(role)
	cfg.TargetRoles = fakeTargets
	next := &proxyUpstream{}
	rec := httptest.NewRecorder()
	ProxyHandler(next, cfg).ServeHTTP(rec, req)
	return next, rec
}

// ownAccountRefusals are the requests that disable, delete or change the role
// of one account, each sent below at the caller's own account and another's.
var ownAccountRefusals = []struct{ name, method, suffix, body string }{
	{"ban", http.MethodPut, "", `{"ban_duration":"876000h"}`},
	{"ban, key in another case", http.MethodPut, "/", `{"BAN_DURATION":"1h"}`},
	{"delete", http.MethodDelete, "", ""},
	{"soft delete", http.MethodDelete, "/", `{"should_soft_delete":true}`},
	{"project role", http.MethodPut, "", `{"role":"service_role"}`},
	{"studio role claim", http.MethodPut, "", `{"app_metadata":{"role":"admin"}}`},
	{"studio role claim removed", http.MethodPut, "", `{"app_metadata":{"role":null}}`},
	{"studio role claim as an object", http.MethodPut, "", `{"app_metadata":{"role":{"name":"admin"}}}`},
}

// Owner decision: nobody disables, deletes or changes the role of their own
// account, admin or not. Studio hides those actions on your own row; a direct
// call is refused here.
func TestNoOneMayDisableDeleteOrReRoleTheirOwnAccount(t *testing.T) {
	for _, role := range []string{"developer", "admin"} {
		for _, id := range []string{ownUserID, strings.ToUpper(ownUserID)} {
			for _, a := range ownAccountRefusals {
				next, rec := sendOwnAccount(t, role, a.method, "/auth/v1/admin/users/"+id+a.suffix, a.body)
				if next.reached || rec.Code != http.StatusForbidden || !refusedAsOwnAccount(rec) {
					t.Errorf("%s %s on %s: got %d %s, reached %v", role, a.name, id, rec.Code, rec.Body.String(), next.reached)
				}
			}
		}
	}
}

// refusedAsOwnAccount reports whether a response is the own-account refusal,
// code and sentence where every Studio refusal puts them.
func refusedAsOwnAccount(rec *httptest.ResponseRecorder) bool {
	var body struct{ Error, Message string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		return false
	}
	return body.Error == "forbidden" && body.Message == refusedOwnAccount
}

// The same requests at another user who is not a Studio admin still pass.
func TestTheOwnAccountRefusalsStillPassForAnotherUser(t *testing.T) {
	for _, role := range []string{"developer", "admin"} {
		for _, a := range ownAccountRefusals {
			next, rec := sendOwnAccount(t, role, a.method, "/auth/v1/admin/users/"+plainUserID+a.suffix, a.body)
			if !next.reached {
				t.Errorf("%s %s on another user: refused with %d %s", role, a.name, rec.Code, rec.Body.String())
			}
		}
	}
}

// Everything else on your own account stays yours to do: revoking your own
// sessions, and editing your profile as Studio's form sends it.
func TestYourOwnAccountIsOtherwiseYours(t *testing.T) {
	self := "/auth/v1/admin/users/" + ownUserID
	for _, role := range []string{"developer", "admin"} {
		for _, a := range []struct{ name, method, path, body string }{
			{"session revoke", http.MethodDelete, "/auth/v1/admin/sessions/" + ownSessionID, ""},
			{"profile edit", http.MethodPut, self, `{"email":"me@example.com","role":"authenticated","user_metadata":{"name":"Me"}}`},
			{"password", http.MethodPut, self, `{"password":"a-new-password"}`},
			{"unban", http.MethodPut, self, `{"ban_duration":"none"}`},
			{"unchanged studio role claim", http.MethodPut, self, `{"app_metadata":{"role":"member","plan":"pro"}}`},
			{"mfa reset", http.MethodDelete, self + "/factors/" + plainUserID, ""},
			{"read", http.MethodGet, self, ""},
			{"malformed body, the auth server's to refuse", http.MethodPut, self, `{"ban_duration":`},
		} {
			if next, rec := sendOwnAccount(t, role, a.method, a.path, a.body); !next.reached {
				t.Errorf("%s %s: refused with %d %s", role, a.name, rec.Code, rec.Body.String())
			}
		}
	}
}

// Owner decision: nobody changes their own Studio role or revokes their own
// Studio access, admin or not, however their id is written. Refused before the
// store is asked, so no database is needed to see it.
func TestNoOneMayChangeTheirOwnStudioMembership(t *testing.T) {
	token := bearer(t, jwt.MapClaims{"sub": ownUserID, "role": "authenticated"})
	for _, role := range []string{"developer", "admin"} {
		for _, id := range []string{ownUserID, strings.ToUpper(ownUserID), "{" + ownUserID + "}"} {
			for _, method := range []string{http.MethodPatch, http.MethodDelete} {
				req := httptest.NewRequest(method, "/admin/studio-members/"+id, strings.NewReader(`{"role":"viewer"}`))
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				MembersAPI(membershipConfig(role)).ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden || !refusedAsOwnAccount(rec) {
					t.Errorf("%s %s own %s: got %d %s", role, method, id, rec.Code, rec.Body.String())
				}
			}
		}
	}
}
