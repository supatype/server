package studioauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
	"github.com/supatype/server/internal/studiomembers"
)

// Who may grant Studio access, and to whom. Reading the list is one capability
// and changing it is another: a developer may see who is in the project through
// their schema visibility, and only an admin may change it.

const (
	memberAdmin  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	memberSecond = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	memberPlain  = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

// membersConfig builds the API over a project database with these users.
//
// Run the DB-backed packages with -p 1: they share one database and the same
// `_supatype` table names.
func membersConfig(t *testing.T, callerRole string) Config {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the membership API against Postgres")
	}
	resources, err := data.Open(context.Background(), &config.Config{SQLDatabaseURL: dsn})
	if err != nil {
		t.Fatalf("open resources: %v", err)
	}
	t.Cleanup(func() { _ = resources.Close() })

	ctx := context.Background()
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}

	for _, stmt := range []string{
		`CREATE SCHEMA IF NOT EXISTS _supatype`,
		`CREATE SCHEMA IF NOT EXISTS auth`,
		`DROP TABLE IF EXISTS _supatype.studio_members`,
		`DROP TABLE IF EXISTS _supatype.studio_audit`,
		`DROP TABLE IF EXISTS auth.users`,
		`CREATE TABLE auth.users (id UUID PRIMARY KEY, email TEXT)`,
		`CREATE TABLE _supatype.studio_members (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID UNIQUE, platform_user_id UUID UNIQUE, role TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT studio_members_one_identity
				CHECK (num_nonnulls(user_id, platform_user_id) = 1))`,
		`CREATE TABLE _supatype.studio_audit (
			id BIGSERIAL PRIMARY KEY, actor_id UUID, target_id UUID NOT NULL,
			action TEXT NOT NULL, role TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`INSERT INTO auth.users (id, email) VALUES
			('` + memberAdmin + `', 'admin@example.com'),
			('` + memberSecond + `', 'second@example.com'),
			('` + memberPlain + `', 'plain@example.com')`,
		`INSERT INTO _supatype.studio_members (user_id, role) VALUES
			('` + memberAdmin + `', 'admin'), ('` + memberSecond + `', 'admin')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, table := range []string{"_supatype.studio_members", "_supatype.studio_audit", "auth.users"} {
			_, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
		}
	})

	c := membershipConfig(callerRole)
	c.Resources = resources
	c.Members = studiomembers.NewStore(resources)
	return c
}

// asAdmin is a token for the caller who holds the admin membership row.
func asAdmin(t *testing.T) string {
	t.Helper()
	return bearer(t, jwt.MapClaims{"sub": memberAdmin, "role": "authenticated"})
}

// membersCall runs one request through the API.
func membersCall(t *testing.T, c Config, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	MembersAPI(c).ServeHTTP(rec, req)
	return rec
}

// ─── The role catalogue ───────────────────────────────────────────────────────

// The catalogue is not sensitive, but an unauthenticated caller still has no
// business enumerating the model.
func TestTheRoleCatalogue(t *testing.T) {
	c := membersConfig(t, "developer")

	rec := membersCall(t, c, http.MethodGet, "/admin/studio-roles", asAdmin(t), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Roles []json.RawMessage `json:"roles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Roles) == 0 {
		t.Error("the catalogue is empty")
	}

	if rec := membersCall(t, c, http.MethodGet, "/admin/studio-roles", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status = %d, want 401", rec.Code)
	}
	if rec := membersCall(t, c, http.MethodPost, "/admin/studio-roles", asAdmin(t), "{}"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status = %d, want 405", rec.Code)
	}
}

// ─── Listing ──────────────────────────────────────────────────────────────────

// Seeing who is in the project is one capability; changing it is another.
func TestListingMembership(t *testing.T) {
	c := membersConfig(t, "developer")

	rec := membersCall(t, c, http.MethodGet, "/admin/studio-members", asAdmin(t), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Members []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Members) != 2 {
		t.Errorf("members = %+v", body.Members)
	}

	// A listing addressed at one user is not a route this API has.
	if rec := membersCall(t, c, http.MethodGet, "/admin/studio-members/"+memberPlain, asAdmin(t), ""); rec.Code != http.StatusNotFound {
		t.Errorf("one user: status = %d, want 404", rec.Code)
	}
}

// A membership table that cannot be read is reported: an empty list would say
// the project has no admins, which is a different and alarming claim.
func TestListingWithNoMembershipTable(t *testing.T) {
	c := membersConfig(t, "developer")
	pool, err := c.Resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `DROP TABLE _supatype.studio_members`); err != nil {
		t.Fatal(err)
	}

	// The caller's own capability comes from the config's StudioRole, so the
	// admission still succeeds and the listing is what fails.
	rec := membersCall(t, c, http.MethodGet, "/admin/studio-members", asAdmin(t), "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (%s)", rec.Code, rec.Body.String())
	}
}

// ─── Granting ─────────────────────────────────────────────────────────────────

func TestSettingARole(t *testing.T) {
	c := membersConfig(t, "admin")

	rec := membersCall(t, c, http.MethodPatch, "/admin/studio-members/"+memberPlain,
		asAdmin(t), `{"role":"editor"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	if role, ok := c.Members.Lookup(memberPlain); !ok || role != "editor" {
		t.Errorf("stored role = %q, ok = %v", role, ok)
	}

	// And it is recorded.
	pool, err := c.Resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM _supatype.studio_audit WHERE action = 'set_role'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("audit rows = %d, want 1", count)
	}
}

func TestSettingARoleRefusals(t *testing.T) {
	c := membersConfig(t, "admin")

	for name, tc := range map[string]struct {
		path, body string
		want       int
	}{
		"no user id":              {"/admin/studio-members", `{"role":"editor"}`, http.StatusBadRequest},
		"a body that is not JSON": {"/admin/studio-members/" + memberPlain, "{", http.StatusBadRequest},
		"a role nobody knows":     {"/admin/studio-members/" + memberPlain, `{"role":"sudo"}`, http.StatusBadRequest},
		"no role named":           {"/admin/studio-members/" + memberPlain, `{}`, http.StatusBadRequest},
		"a user who does not exist": {
			"/admin/studio-members/dddddddd-dddd-dddd-dddd-dddddddddddd",
			`{"role":"editor"}`, http.StatusNotFound,
		},
	} {
		rec := membersCall(t, c, http.MethodPatch, tc.path, asAdmin(t), tc.body)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// ─── Revoking ─────────────────────────────────────────────────────────────────

func TestRevoking(t *testing.T) {
	c := membersConfig(t, "admin")

	rec := membersCall(t, c, http.MethodDelete, "/admin/studio-members/"+memberSecond, asAdmin(t), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if _, ok := c.Members.Lookup(memberSecond); ok {
		t.Error("the membership survived")
	}

	// And the last admin cannot then be revoked, because nobody would be able to
	// grant access to anyone again.
	rec = membersCall(t, c, http.MethodDelete, "/admin/studio-members/"+memberAdmin, asAdmin(t), "")
	if rec.Code == http.StatusOK {
		t.Error("the last admin was revoked")
	}
}

func TestRevokingWithNoUserID(t *testing.T) {
	c := membersConfig(t, "admin")

	rec := membersCall(t, c, http.MethodDelete, "/admin/studio-members", asAdmin(t), "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

// ─── Methods ──────────────────────────────────────────────────────────────────

func TestAnUnsupportedMethodOnMembership(t *testing.T) {
	c := membersConfig(t, "admin")

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		rec := membersCall(t, c, method, "/admin/studio-members/"+memberPlain, asAdmin(t), "{}")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, rec.Code)
		}
	}
}

// ─── Elevated requests are recorded ───────────────────────────────────────────

// A read that bypasses RLS is logged only — Studio polls, and a row per read
// would bury the membership trail. Anything that could change data gets a
// durable row, because "who edited this outside their own policies" has to
// survive log rotation.
func TestOnlyWritesAreAudited(t *testing.T) {
	c := membersConfig(t, "admin")
	token := asAdmin(t)

	next := &proxyUpstream{}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		ProxyHandler(next, c).ServeHTTP(httptest.NewRecorder(),
			studioRequest(method, "/rest/v1/posts", token))
	}

	pool, err := c.Resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}
	countElevated := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM _supatype.studio_audit WHERE action = 'elevated_request'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := countElevated(); got != 0 {
		t.Errorf("reads produced %d audit rows, want none", got)
	}

	// The audit table this path writes carries a detail column and no target.
	if _, err := pool.Exec(context.Background(), `ALTER TABLE _supatype.studio_audit
		ADD COLUMN IF NOT EXISTS detail JSONB,
		ALTER COLUMN target_id DROP NOT NULL`); err != nil {
		t.Fatal(err)
	}

	ProxyHandler(next, c).ServeHTTP(httptest.NewRecorder(),
		studioRequest(http.MethodPost, "/rest/v1/posts", token))

	if got := countElevated(); got != 1 {
		t.Errorf("a write produced %d audit rows, want 1", got)
	}
}

// A role with neither capability sees nothing of the membership model, and one
// that may read it still may not change it.
func TestReadingAndChangingAreSeparateCapabilities(t *testing.T) {
	for role, want := range map[string]struct{ read, write int }{
		"admin":     {http.StatusOK, http.StatusOK},
		"developer": {http.StatusOK, http.StatusForbidden},
		"editor":    {http.StatusForbidden, http.StatusForbidden},
	} {
		c := membersConfig(t, role)

		if rec := membersCall(t, c, http.MethodGet, "/admin/studio-members", asAdmin(t), ""); rec.Code != want.read {
			t.Errorf("%s reading: status = %d, want %d (%s)", role, rec.Code, want.read, rec.Body.String())
		}
		rec := membersCall(t, c, http.MethodPatch, "/admin/studio-members/"+memberPlain,
			asAdmin(t), `{"role":"editor"}`)
		if rec.Code != want.write {
			t.Errorf("%s changing: status = %d, want %d (%s)", role, rec.Code, want.write, rec.Body.String())
		}
	}
}

// The last admin cannot be demoted or revoked by anyone, including another
// admin: there would then be nobody able to grant access again.
func TestTheLastAdminIsProtected(t *testing.T) {
	c := membersConfig(t, "admin")

	// memberSecond goes first, leaving memberAdmin as the only one.
	if rec := membersCall(t, c, http.MethodDelete, "/admin/studio-members/"+memberSecond,
		asAdmin(t), ""); rec.Code != http.StatusOK {
		t.Fatalf("revoking the second admin: %d %s", rec.Code, rec.Body.String())
	}

	// Asked by somebody else, so this is the last-admin guard rather than the
	// refusal to change your own role.
	asSecond := bearer(t, jwt.MapClaims{"sub": memberSecond, "role": "authenticated"})

	for name, tc := range map[string]struct{ method, body string }{
		"demoting": {http.MethodPatch, `{"role":"editor"}`},
		"revoking": {http.MethodDelete, ""},
	} {
		rec := membersCall(t, c, tc.method, "/admin/studio-members/"+memberAdmin, asSecond, tc.body)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s the last admin: status = %d, want 409 (%s)", name, rec.Code, rec.Body.String())
		}
	}

	if role, ok := c.Members.Lookup(memberAdmin); !ok || role != "admin" {
		t.Errorf("the last admin lost their role: %q %v", role, ok)
	}
}

// A caller who may not change membership is refused on the delete route too,
// not only on the patch.
func TestRevokingNeedsTheCapability(t *testing.T) {
	c := membersConfig(t, "developer")

	rec := membersCall(t, c, http.MethodDelete, "/admin/studio-members/"+memberSecond, asAdmin(t), "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if _, ok := c.Members.Lookup(memberSecond); !ok {
		t.Error("the membership was revoked despite the refusal")
	}
}

// Under the bypass there is no caller to check, and the change is recorded with
// no actor rather than a made-up one.
func TestMembershipUnderDevBypass(t *testing.T) {
	c := membersConfig(t, "admin")
	c.Mode = "dev"
	c.OpenDev = true

	rec := membersCall(t, c, http.MethodPatch, "/admin/studio-members/"+memberPlain, "", `{"role":"editor"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	if role, ok := c.Members.Lookup(memberPlain); !ok || role != "editor" {
		t.Errorf("role = %q, ok = %v", role, ok)
	}
}
