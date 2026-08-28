package studioauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/dbpool"
	"github.com/supatype/server/internal/studiomembers"
)

// The whole capability path over HTTP, against a real Postgres: token proves
// identity, `_supatype.studio_members` decides admission.
//
// Skipped unless SUPATYPE_TEST_DSN points at a throwaway database. Run the
// DB-backed packages with `-p 1`: they share one database and the same
// `_supatype` table names, so `go test`'s default per-package parallelism has
// them dropping each other's tables mid-run.
func TestVerifyHandlerUsesMembershipNotClaims(t *testing.T) {
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run Studio verification against Postgres")
	}
	dbpool.Configure(dsn)

	const member = "11111111-2222-3333-4444-555555555555"
	const claimant = "99999999-8888-7777-6666-555555555555"

	ctx := context.Background()
	pool, err := dbpool.Pool(ctx)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	for _, stmt := range []string{
		`CREATE SCHEMA IF NOT EXISTS _supatype`,
		`DROP TABLE IF EXISTS _supatype.studio_members`,
		`CREATE TABLE _supatype.studio_members (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID UNIQUE,
			platform_user_id UUID UNIQUE,
			role TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT studio_members_one_identity
				CHECK (num_nonnulls(user_id, platform_user_id) = 1))`,
		`INSERT INTO _supatype.studio_members (user_id, role) VALUES ('` + member + `', 'admin')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS _supatype.studio_members`)
	})

	cfg := Config{
		JWTSecret:  testSecret,
		AdminRoles: DefaultAdminRoles,
		StudioRole: studiomembers.Lookup,
	}
	handler := VerifyHandler(cfg)

	call := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/studio/auth/verify", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// A member with no admin claim at all is admitted: capability is membership.
	memberToken := signClaims(jwt.MapClaims{
		"sub":  member,
		"role": "authenticated",
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	if code := call(memberToken); code != http.StatusOK {
		t.Fatalf("expected 200 for a member with no claim, got %d", code)
	}

	// The escalation this closes: `app_metadata` is the developer's namespace, so
	// an app role of "admin" must not open Studio.
	claimToken := signClaims(jwt.MapClaims{
		"sub":          claimant,
		"role":         "authenticated",
		"app_metadata": map[string]interface{}{"role": "admin"},
		"exp":          time.Now().Add(time.Hour).Unix(),
	})
	if code := call(claimToken); code != http.StatusForbidden {
		t.Fatalf("expected 403 for an app_metadata.role=admin non-member, got %d", code)
	}

	// Revocation takes effect on the next request — no cached capability.
	if _, err := pool.Exec(ctx,
		`DELETE FROM _supatype.studio_members WHERE user_id = $1::uuid`, member); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if code := call(memberToken); code != http.StatusForbidden {
		t.Fatalf("expected 403 after revocation, got %d", code)
	}
}
