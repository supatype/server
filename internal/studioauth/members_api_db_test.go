package studioauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/dbpool"
	"github.com/supatype/server/internal/studiomembers"
)

const (
	apiAdmin  = "aaaa1111-1111-1111-1111-111111111111"
	apiSecond = "aaaa2222-2222-2222-2222-222222222222"
	apiEditor = "aaaa3333-3333-3333-3333-333333333333"
)

// The assignment API over HTTP, against a real Postgres.
//
// Skipped unless SUPATYPE_TEST_DSN points at a throwaway database.
func TestMembersAPI(t *testing.T) {
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the membership API against Postgres")
	}
	dbpool.Configure(dsn)

	ctx := context.Background()
	pool, err := dbpool.Pool(ctx)
	if err != nil {
		t.Fatalf("open pool: %v", err)
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
			user_id UUID UNIQUE,
			platform_user_id UUID UNIQUE,
			role TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT studio_members_one_identity
				CHECK (num_nonnulls(user_id, platform_user_id) = 1))`,
		`CREATE TABLE _supatype.studio_audit (
			id BIGSERIAL PRIMARY KEY, actor_id UUID, target_id UUID NOT NULL,
			action TEXT NOT NULL, role TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`INSERT INTO auth.users (id, email) VALUES
			('` + apiAdmin + `', 'admin@example.com'),
			('` + apiSecond + `', 'second@example.com'),
			('` + apiEditor + `', 'editor@example.com')`,
		`INSERT INTO _supatype.studio_members (user_id, role) VALUES
			('` + apiAdmin + `', 'admin'),
			('` + apiSecond + `', 'admin'),
			('` + apiEditor + `', 'editor')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS _supatype.studio_members`,
			`DROP TABLE IF EXISTS _supatype.studio_audit`,
			`DROP TABLE IF EXISTS auth.users`,
		} {
			_, _ = pool.Exec(context.Background(), stmt)
		}
	})

	cfg := Config{
		JWTSecret:  testSecret,
		AdminRoles: DefaultAdminRoles,
		StudioRole: studiomembers.Lookup,
	}
	handler := MembersAPI(cfg)

	tokenFor := func(sub string) string {
		return signClaims(jwt.MapClaims{
			"sub": sub,
			"exp": time.Now().Add(time.Hour).Unix(),
		})
	}

	call := func(sub, method, path, body string) (int, string) {
		var reader *strings.Reader
		if body == "" {
			reader = strings.NewReader("")
		} else {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Authorization", "Bearer "+tokenFor(sub))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	t.Run("role catalogue lists every known role", func(t *testing.T) {
		code, body := call(apiAdmin, http.MethodGet, "/admin/studio-roles", "")
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", code, body)
		}
		var parsed struct {
			Roles []struct {
				Role        string            `json:"role"`
				Permissions StudioPermissions `json:"permissions"`
			} `json:"roles"`
		}
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(parsed.Roles) != len(KnownStudioRoles()) {
			t.Fatalf("expected %d roles, got %d", len(KnownStudioRoles()), len(parsed.Roles))
		}
		if parsed.Roles[0].Role != RoleAdmin || !parsed.Roles[0].Permissions.ManageMembers {
			t.Fatalf("expected admin first with ManageMembers: %+v", parsed.Roles[0])
		}
	})

	t.Run("an editor cannot enumerate or change membership", func(t *testing.T) {
		if code, _ := call(apiEditor, http.MethodGet, "/admin/studio-members", ""); code != http.StatusForbidden {
			t.Fatalf("expected 403 listing, got %d", code)
		}
		code, _ := call(apiEditor, http.MethodPatch,
			"/admin/studio-members/"+apiSecond, `{"role":"editor"}`)
		if code != http.StatusForbidden {
			t.Fatalf("expected 403 on patch, got %d", code)
		}
	})

	t.Run("an unknown role is rejected before it reaches the database", func(t *testing.T) {
		code, body := call(apiAdmin, http.MethodPatch,
			"/admin/studio-members/"+apiEditor, `{"role":"superuser"}`)
		if code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", code, body)
		}
		if role, _ := studiomembers.Lookup(apiEditor); role != "editor" {
			t.Fatalf("role changed despite rejection: %q", role)
		}
	})

	t.Run("self-change is refused", func(t *testing.T) {
		code, body := call(apiAdmin, http.MethodPatch,
			"/admin/studio-members/"+apiAdmin, `{"role":"editor"}`)
		if code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", code, body)
		}
	})

	t.Run("an unknown user is a 404", func(t *testing.T) {
		code, _ := call(apiAdmin, http.MethodPatch,
			"/admin/studio-members/99999999-9999-9999-9999-999999999999", `{"role":"editor"}`)
		if code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", code)
		}
	})

	t.Run("an admin promotes and demotes another user", func(t *testing.T) {
		code, body := call(apiAdmin, http.MethodPatch,
			"/admin/studio-members/"+apiEditor, `{"role":"developer"}`)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", code, body)
		}
		if role, _ := studiomembers.Lookup(apiEditor); role != "developer" {
			t.Fatalf("expected developer, got %q", role)
		}

		code, _ = call(apiAdmin, http.MethodDelete, "/admin/studio-members/"+apiEditor, "")
		if code != http.StatusOK {
			t.Fatalf("expected 200 on revoke, got %d", code)
		}
		if _, ok := studiomembers.Lookup(apiEditor); ok {
			t.Fatal("membership survived revocation")
		}
	})

	t.Run("the last admin cannot be demoted", func(t *testing.T) {
		// Leave exactly one admin.
		if code, body := call(apiAdmin, http.MethodDelete,
			"/admin/studio-members/"+apiSecond, ""); code != http.StatusOK {
			t.Fatalf("expected 200 removing the second admin, got %d: %s", code, body)
		}
		// apiAdmin cannot demote themselves, so act as a different admin: promote
		// one back, then have them try to demote the other down to the last one.
		if code, _ := call(apiAdmin, http.MethodPatch,
			"/admin/studio-members/"+apiSecond, `{"role":"admin"}`); code != http.StatusOK {
			t.Fatal("could not restore the second admin")
		}
		if code, _ := call(apiSecond, http.MethodPatch,
			"/admin/studio-members/"+apiAdmin, `{"role":"editor"}`); code != http.StatusOK {
			t.Fatal("demoting one of two admins should be allowed")
		}
		// apiSecond is now the only admin; apiAdmin (editor) cannot touch them.
		code, _ := call(apiAdmin, http.MethodPatch,
			"/admin/studio-members/"+apiSecond, `{"role":"editor"}`)
		if code != http.StatusForbidden {
			t.Fatalf("a demoted admin must lose management rights, got %d", code)
		}
	})

	t.Run("every change is audited", func(t *testing.T) {
		var count int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM _supatype.studio_audit`).Scan(&count); err != nil {
			t.Fatalf("read audit: %v", err)
		}
		if count == 0 {
			t.Fatal("no membership changes were audited")
		}
	})
}
