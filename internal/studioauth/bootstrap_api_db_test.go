package studioauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/dbpool"
	"github.com/supatype/server/internal/studiomembers"
)

const bootstrapUser = "bbbb1111-1111-1111-1111-111111111111"

const bootstrapAST = `{
  "models": [
    {
      "name": "Post",
      "annotations": {
        "db": { "tableName": "posts" },
        "platform": { "access": {
          "read": {"type":"public"},
          "update": {"type":"owner","field":"author_id"}
        } }
      }
    },
    {
      "name": "AuditLog",
      "annotations": {
        "db": { "tableName": "audit_log" },
        "platform": { "access": { "read": {"type":"role","roles":["auditor"]} } }
      }
    }
  ]
}`

// The bootstrap endpoints over HTTP, against a real Postgres.
//
// Skipped unless SUPATYPE_TEST_DSN points at a throwaway database. Run the
// DB-backed packages with `-p 1` — they share one database and the same
// `_supatype` table names.
func TestBootstrapEndpoints(t *testing.T) {
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the bootstrap endpoints against Postgres")
	}
	t.Setenv("SUPATYPE_SQL_DATABASE_URL", dsn)
	t.Setenv("SUPATYPE_MODE", "")
	t.Setenv("STUDIO_OPEN_DEV", "")

	ctx := context.Background()
	pool, err := dbpool.Pool(ctx)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	for _, stmt := range []string{
		`CREATE SCHEMA IF NOT EXISTS _supatype`,
		`DROP TABLE IF EXISTS _supatype.schema_state`,
		`DROP TABLE IF EXISTS _supatype.studio_members`,
		`CREATE TABLE _supatype.schema_state (
			id INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
			db_state JSONB NOT NULL,
			ast_snapshot JSONB,
			admin_config JSONB,
			captured_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			engine_version TEXT NOT NULL)`,
		`CREATE TABLE _supatype.studio_members (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID UNIQUE,
			platform_user_id UUID UNIQUE,
			role TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT studio_members_one_identity
				CHECK (num_nonnulls(user_id, platform_user_id) = 1))`,
		`INSERT INTO _supatype.studio_members (user_id, role) VALUES ('` + bootstrapUser + `', 'editor')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO _supatype.schema_state (id, db_state, ast_snapshot, engine_version)
		 VALUES (1, '{}'::jsonb, $1::jsonb, 'test')`, bootstrapAST); err != nil {
		t.Fatalf("seed schema state: %v", err)
	}
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS _supatype.schema_state`,
			`DROP TABLE IF EXISTS _supatype.studio_members`,
		} {
			_, _ = pool.Exec(context.Background(), stmt)
		}
	})

	cfg := Config{
		JWTSecret:  testSecret,
		AdminRoles: DefaultAdminRoles,
		StudioRole: studiomembers.Lookup,
	}

	tokenFor := func(appRole string) string {
		return signClaims(jwt.MapClaims{
			"sub":          bootstrapUser,
			"role":         "authenticated",
			"app_metadata": map[string]interface{}{"role": appRole},
			"exp":          time.Now().Add(time.Hour).Unix(),
		})
	}

	call := func(handler http.HandlerFunc, token, ifNoneMatch string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/studio/schema", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	schema := SchemaHandler(cfg)
	session := SessionHandler(cfg)

	t.Run("refuses an unauthenticated caller", func(t *testing.T) {
		if code := call(schema, "", "").Code; code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", code)
		}
	})

	// Filtering is per caller and happens here: a model no operation can reach is
	// omitted, so the response does not disclose that the table exists.
	t.Run("filters the schema to what the caller may reach", func(t *testing.T) {
		rec := call(schema, tokenFor("authenticated"), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			SchemaHash string `json:"schemaHash"`
			Models     []struct {
				Table  string            `json:"table"`
				Access map[string]string `json:"access"`
			} `json:"models"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.SchemaHash == "" {
			t.Fatal("no schema hash to cache on")
		}
		if len(body.Models) != 1 || body.Models[0].Table != "posts" {
			t.Fatalf("expected only posts, got %+v", body.Models)
		}
		if body.Models[0].Access["read"] != "allow" {
			t.Errorf("read: got %q", body.Models[0].Access["read"])
		}
		if body.Models[0].Access["update"] != "row" {
			t.Errorf("update should depend on the row, got %q", body.Models[0].Access["update"])
		}

		// The auditor sees the second table; the same schema, a different answer.
		auditor := call(schema, tokenFor("auditor"), "")
		var auditorBody struct {
			Models []struct{ Table string } `json:"models"`
		}
		_ = json.Unmarshal(auditor.Body.Bytes(), &auditorBody)
		if len(auditorBody.Models) != 2 {
			t.Fatalf("an auditor should see both tables, got %+v", auditorBody.Models)
		}
	})

	// The payload depends on the caller as well as the schema, so two callers with
	// different access must not share a cache entry.
	t.Run("revalidates with an ETag that includes the caller", func(t *testing.T) {
		first := call(schema, tokenFor("authenticated"), "")
		etag := first.Header().Get("ETag")
		if etag == "" {
			t.Fatal("no ETag to revalidate against")
		}

		again := call(schema, tokenFor("authenticated"), etag)
		if again.Code != http.StatusNotModified {
			t.Fatalf("expected 304, got %d", again.Code)
		}

		other := call(schema, tokenFor("auditor"), etag)
		if other.Code == http.StatusNotModified {
			t.Fatal("a different caller must not match another caller's ETag")
		}
	})

	t.Run("session reports capability, mode and settled access", func(t *testing.T) {
		rec := call(session, tokenFor("authenticated"), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Sub        string                       `json:"sub"`
			Role       string                       `json:"role"`
			AppRole    string                       `json:"appRole"`
			Mode       string                       `json:"mode"`
			CanElevate bool                         `json:"canElevate"`
			Access     map[string]map[string]string `json:"access"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if body.Sub != bootstrapUser {
			t.Errorf("sub: got %q", body.Sub)
		}
		// The Studio role and the application role are separate namespaces, and the
		// session reports both — a rule tests the app role, so conflating them would
		// report access the database will not grant.
		if body.Role != "editor" {
			t.Errorf("studio role: got %q", body.Role)
		}
		if body.AppRole != "authenticated" {
			t.Errorf("app role: got %q", body.AppRole)
		}
		// An editor cannot elevate, so it acts as itself.
		if body.Mode != ModeSelf || body.CanElevate {
			t.Errorf("mode=%q canElevate=%v", body.Mode, body.CanElevate)
		}
		if body.Access["posts"]["read"] != "allow" {
			t.Errorf("posts read: got %q", body.Access["posts"]["read"])
		}
		if _, leaked := body.Access["audit_log"]; leaked {
			t.Error("session disclosed a table the caller cannot reach")
		}
	})

	// A never-pushed project has nothing to render, which is a different answer
	// from "you may not see it".
	t.Run("reports a missing schema state distinctly", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE _supatype.schema_state SET ast_snapshot = NULL`); err != nil {
			t.Fatalf("clear snapshot: %v", err)
		}
		defer func() {
			_, _ = pool.Exec(ctx, `UPDATE _supatype.schema_state SET ast_snapshot = $1::jsonb`, bootstrapAST)
		}()

		if code := call(schema, tokenFor("authenticated"), "").Code; code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", code)
		}
	})
}
