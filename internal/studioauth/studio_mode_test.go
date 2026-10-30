package studioauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// Studio roles decide what a Studio user may do in Studio (owner decision
// 2026-10-21): the app's row rules do not limit Studio editing, its field rules
// still apply. These tests are the proxy's half of that; the engine's Studio
// policies and the masking proof are in the schema engine's
// `tests/studio_editing_tests.rs`.

// studioClaims verifies the token a request acting as Studio carried upstream
// and returns its claims, failing unless it carries the Studio claim.
func studioClaims(t *testing.T, authorization string) jwt.MapClaims {
	t.Helper()
	token, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok {
		t.Fatalf("authorization %q is not a bearer token", authorization)
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (interface{}, error) {
		return []byte(testSecret), nil
	}); err != nil {
		t.Fatalf("the forwarded token does not verify: %v (%q)", err, authorization)
	}
	if _, ok := claims[StudioClaim]; !ok {
		t.Fatalf("the forwarded token carries no %s claim: %v", StudioClaim, claims)
	}
	return claims
}

// proxyAs sends one request through the proxy as a member of role, with the
// acting mode asked for (none when empty), and returns what reached upstream.
func proxyAs(t *testing.T, role, mode, method, path string) (*proxyUpstream, *httptest.ResponseRecorder) {
	t.Helper()
	token := bearer(t, jwt.MapClaims{"sub": "user-1", "role": "authenticated"})
	req := studioRequest(method, path, token)
	if mode != "" {
		req.Header.Set(ActingModeHeader, mode)
	}
	next := &proxyUpstream{}
	rec := httptest.NewRecorder()
	ProxyHandler(next, membershipConfig(role)).ServeHTTP(rec, req)
	return next, rec
}

// recorded is the elevated-request log entry for path, or nil.
func recorded(hook *logtest.Hook, path string) logrus.Fields {
	for _, entry := range hook.AllEntries() {
		if entry.Message == "studio: elevated request" && entry.Data["path"] == path {
			return entry.Data
		}
	}
	return nil
}

// Regression: an editor saving a page draft on kitchen-sink got `new row
// violates row-level security policy`, because their own app token went
// upstream and the app's rules decided. Their write now goes as Studio: their
// own identity, which every field rule reads, plus the claim the engine's
// Studio policies admit. And it is recorded with who, which role and where.
func TestAnEditorWritesRowsAsStudioAndIsRecorded(t *testing.T) {
	for _, path := range []string{"/rest/v1/page_versions", "/graphql/v1"} {
		hook := logtest.NewGlobal()
		next, rec := proxyAs(t, "editor", "", http.MethodPost, path)
		if !next.reached || rec.Header().Get(ActingModeHeader) != ModeStudio {
			t.Fatalf("%s: got %d mode %q", path, rec.Code, rec.Header().Get(ActingModeHeader))
		}
		claims := studioClaims(t, next.auth)
		if claims["sub"] != "user-1" || claims["role"] != "authenticated" || claims[StudioClaim] != "write" {
			t.Errorf("%s: claims %v, want the caller's identity with write", path, claims)
		}
		if next.apikey != "anon-key" {
			t.Errorf("%s: apikey %q, want the anon key", path, next.apikey)
		}
		entry := recorded(hook, path)
		if entry == nil || entry["actor"] != "user-1" || entry["role"] != "editor" || entry["mode"] != ModeStudio {
			t.Errorf("%s: not recorded with actor, role and mode: %v", path, entry)
		}
		hook.Reset()
	}
}

// A viewer reads every row as Studio, with the read level, and writes none.
func TestAViewerReadsRowsAsStudioAndWritesNone(t *testing.T) {
	next, _ := proxyAs(t, "viewer", "", http.MethodGet, "/rest/v1/posts")
	if claims := studioClaims(t, next.auth); claims[StudioClaim] != "read" {
		t.Errorf("viewer claims %v, want read", claims)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/rest/v1/posts"},
		{http.MethodPatch, "/rest/v1/posts?id=eq.1"},
		{http.MethodDelete, "/rest/v1/posts?id=eq.1"},
		{http.MethodPost, "/storage/v1/object/media/a.png"},
		{http.MethodDelete, "/storage/v1/object/media"},
		{http.MethodPost, "/storage/v1/bucket"},
	} {
		for _, mode := range []string{"", ModeStudio, ModeSelf} {
			if next, rec := proxyAs(t, "viewer", mode, c.method, c.path); next.reached || rec.Code != http.StatusForbidden {
				t.Errorf("viewer %q %s %s: got %d, reached %v", mode, c.method, c.path, rec.Code, next.reached)
			}
		}
	}
}

// Files go as the service role for an editor: list, read, upload, replace and
// delete, and listing buckets, which is where browsing starts. Writes are
// recorded with the editor's role.
func TestAnEditorWorksWithFilesAsStudio(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/storage/v1/bucket"},
		{http.MethodGet, "/storage/v1/object/media/a.png"},
		{http.MethodPost, "/storage/v1/object/list/media"},
		{http.MethodPost, "/storage/v1/object/media/a.png"},
		{http.MethodPut, "/storage/v1/object/media/a.png"},
		{http.MethodDelete, "/storage/v1/object/media"},
	} {
		hook := logtest.NewGlobal()
		next, rec := proxyAs(t, "editor", "", c.method, c.path)
		if !next.reached || next.auth != "Bearer service-role-key" {
			t.Errorf("editor %s %s: got %d with %q", c.method, c.path, rec.Code, next.auth)
		}
		if entry := recorded(hook, c.path); entry == nil || entry["role"] != "editor" || entry["mode"] != ModeStudio {
			t.Errorf("editor %s %s: not recorded: %v", c.method, c.path, entry)
		}
		hook.Reset()
	}
}

// A viewer browses files as Studio: listing a bucket and signing a download are
// POSTs that only read.
func TestAViewerBrowsesFiles(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/storage/v1/bucket"},
		{http.MethodPost, "/storage/v1/object/list/media"},
		{http.MethodPost, "/storage/v1/object/sign/media/folder/a.png"},
		{http.MethodGet, "/storage/v1/object/media/a.png"},
	} {
		if next, rec := proxyAs(t, "viewer", "", c.method, c.path); !next.reached || next.auth != "Bearer service-role-key" {
			t.Errorf("viewer %s %s: got %d with %q", c.method, c.path, rec.Code, next.auth)
		}
	}
}

// Bucket management is the developer's and the admin's, never the editor's,
// whatever mode the editor asks for.
func TestAnEditorCannotManageBuckets(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/storage/v1/bucket"},
		{http.MethodPut, "/storage/v1/bucket/media"},
		{http.MethodDelete, "/storage/v1/bucket/media"},
		{http.MethodPost, "/storage/v1/bucket/media/empty"},
	} {
		for _, mode := range []string{"", ModeStudio, ModeSelf, ModeElevated} {
			if next, rec := proxyAs(t, "editor", mode, c.method, c.path); next.reached || rec.Code != http.StatusForbidden {
				t.Errorf("editor %q %s %s: got %d, reached %v", mode, c.method, c.path, rec.Code, next.reached)
			}
		}
	}
}

// A developer manages buckets and the project's users, and runs SQL, all as the
// service role. Granting Studio roles stays the admin's.
func TestADeveloperAdministersBucketsUsersAndSQL(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/storage/v1/bucket"},
		{http.MethodDelete, "/storage/v1/bucket/media"},
		{http.MethodGet, "/auth/v1/admin/users"},
		{http.MethodPost, "/auth/v1/admin/users"},
		{http.MethodPost, "/sql"},
	} {
		next, rec := proxyAs(t, "developer", "", c.method, c.path)
		if !next.reached || next.auth != "Bearer service-role-key" {
			t.Errorf("developer %s %s: got %d with %q", c.method, c.path, rec.Code, next.auth)
		}
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/admin/studio-members"},
		{http.MethodPatch, "/admin/studio-members/abc"},
		{http.MethodDelete, "/admin/studio-members/abc"},
	} {
		if next, rec := proxyAs(t, "developer", "", c.method, c.path); next.reached || rec.Code != http.StatusForbidden {
			t.Errorf("developer %s %s: got %d, reached %v", c.method, c.path, rec.Code, next.reached)
		}
	}
}

// Project keys are the developer's too: Studio shows them from the permission
// verify reports, and Cloud's key routes check the same document.
func TestADeveloperHoldsProjectKeys(t *testing.T) {
	for role, want := range map[string]bool{"admin": true, "developer": true, "editor": false, "viewer": false} {
		perms, _ := PermissionsForRole(role)
		if perms.ManageKeys != want {
			t.Errorf("%s: manageKeys = %v, want %v", role, perms.ManageKeys, want)
		}
	}
}

// "Act as myself" keeps working for every role as a preview of the app's rules:
// content goes with the caller's own token, exactly as before Studio mode.
func TestActingAsSelfGoesAsSelfForEveryRole(t *testing.T) {
	for _, role := range KnownStudioRoles() {
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/rest/v1/posts"},
			{http.MethodGet, "/graphql/v1"},
			{http.MethodGet, "/storage/v1/object/media/a.png"},
		} {
			next, rec := proxyAs(t, role, ModeSelf, c.method, c.path)
			if !next.reached || strings.Contains(next.auth, "service-role-key") || rec.Header().Get(ActingModeHeader) != ModeSelf {
				t.Errorf("%s self %s %s: got %d with %q mode %q", role, c.method, c.path, rec.Code, next.auth, rec.Header().Get(ActingModeHeader))
			}
			claims := jwt.MapClaims{}
			if _, err := jwt.ParseWithClaims(strings.TrimPrefix(next.auth, "Bearer "), claims, func(*jwt.Token) (interface{}, error) {
				return []byte(testSecret), nil
			}); err != nil {
				t.Errorf("%s self %s: forwarded %q", role, c.path, next.auth)
			}
			if _, carried := claims[StudioClaim]; carried {
				t.Errorf("%s self %s: carried the Studio claim", role, c.path)
			}
		}
	}
}

// A request acting as Studio carries more than the caller's own token, so a path
// an upstream would resolve differently is refused: `/storage/v1/object/../bucket`
// is a bucket an editor may not create.
func TestStudioRefusesAmbiguousPaths(t *testing.T) {
	for _, path := range []string{
		"/storage/v1/object/x/../../bucket",
		"/storage/v1/object/./../bucket",
		"/storage/v1/object/%2e%2e/bucket",
		"/storage/v1/object/media%2f..%2f..%2fbucket",
		`/storage/v1/object/media\..\..\bucket`,
		"/rest/v1/posts/../../auth/v1/admin/users",
	} {
		next, rec := proxyAs(t, "editor", "", http.MethodPost, path)
		if next.reached || rec.Code != http.StatusForbidden {
			t.Errorf("editor POST %q: got %d, reached %v (%q)", path, rec.Code, next.reached, next.auth)
		}
	}
}

// The token never outlives the session it was made from, nor a minute, and a
// Studio claim the caller's own token carried is replaced, never trusted.
func TestTheStudioTokenIsShortLivedAndItsClaimIsTheGateways(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	// As a verified token decodes them: JSON numbers are float64.
	soon := jwt.MapClaims{"sub": "u", "exp": float64(now.Add(10 * time.Second).Unix()), StudioClaim: "write"}
	later := jwt.MapClaims{"sub": "u", "exp": float64(now.Add(time.Hour).Unix())}
	for name, c := range map[string]struct {
		caller  jwt.MapClaims
		wantExp int64
	}{
		"expiring session": {soon, now.Add(10 * time.Second).Unix()},
		"long session":     {later, now.Add(studioTokenLifetime).Unix()},
	} {
		token, err := mintStudioToken(testSecret, c.caller, false, now)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		claims := jwt.MapClaims{}
		_, _, _ = jwt.NewParser().ParseUnverified(token, claims)
		if exp, _ := claims.GetExpirationTime(); exp == nil || exp.Unix() != c.wantExp {
			t.Errorf("%s: exp %v, want %d", name, exp, c.wantExp)
		}
		if claims[StudioClaim] != "read" {
			t.Errorf("%s: claim %v, want the gateway's read", name, claims[StudioClaim])
		}
	}
	if soon[StudioClaim] != "write" {
		t.Error("minting changed the caller's own claims")
	}
	if _, err := mintStudioToken(testSecret, nil, true, now); err == nil {
		t.Error("a token was minted for no one")
	}
}

// Developers administer the project as admins do: the admin API with the
// database credentials, the control plane, function administration, preview
// links, CORS and any route not named. Only granting Studio roles is the
// admin's alone (owner decision 2026-10-21). Editors and viewers reach none of
// it, whatever mode they ask for.
func TestADeveloperAdministersTheProject(t *testing.T) {
	plane := []struct{ method, path string }{
		{http.MethodPost, "/admin/v1/database/credentials/first-view"},
		{http.MethodGet, "/admin/v1/database/connection"},
		{http.MethodPost, "/platform/v1/projects/ref/schema/push"},
		{http.MethodPost, "/admin/v1/functions/env"},
		{http.MethodPost, "/admin/preview-links"},
		{http.MethodPut, "/admin/cors-origins"},
		{http.MethodGet, "/some/route/added/later"},
	}
	for _, c := range plane {
		if next, rec := proxyAs(t, "developer", "", c.method, c.path); !next.reached || next.auth != "Bearer service-role-key" {
			t.Errorf("developer %s %s: got %d with %q", c.method, c.path, rec.Code, next.auth)
		}
		for _, role := range []string{"editor", "viewer"} {
			for _, mode := range []string{"", ModeStudio, ModeSelf, ModeElevated} {
				if next, rec := proxyAs(t, role, mode, c.method, c.path); next.reached || rec.Code != http.StatusForbidden {
					t.Errorf("%s %q %s %s: got %d, reached %v", role, mode, c.method, c.path, rec.Code, next.reached)
				}
			}
		}
	}
}
