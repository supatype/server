package sqlrunner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/supatype/server/internal/config"
)

// secsqlPools connects to the supatype postgres image, which carries the SQ2
// SECURITY DEFINER helpers and the auth and storage schemas. Plain Postgres has
// neither, so this uses its own DSN and skips without it.
func secsqlPools(t *testing.T) Pools {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_SECSQL_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_SECSQL_DSN (a supatype postgres image) to run the SQ2 admin tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return func() (Pool, error) { return pool, nil }
}

func postAdmin(t *testing.T, h http.Handler, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/studio-admin", strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestExtensionHandlerEnablesOnlyShippedExtensions(t *testing.T) {
	pools := secsqlPools(t)
	cfg := authorised(&config.Config{})
	h := ExtensionHandler(cfg, pools)

	pool, _ := pools()
	defer func() {
		tx, _ := pool.Begin(context.Background())
		_, _ = tx.Exec(context.Background(), "DROP EXTENSION IF EXISTS citext")
		_ = tx.Commit(context.Background())
	}()

	if rec := postAdmin(t, h, `{"name":"citext","enable":true}`, serviceKey); rec.Code != http.StatusOK {
		t.Fatalf("enable citext: %d %s", rec.Code, rec.Body.String())
	}
	if !extensionInstalled(t, pool, "citext") {
		t.Fatal("citext was not installed")
	}
	if rec := postAdmin(t, h, `{"name":"no_such_extension","enable":true}`, serviceKey); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown extension: want 400, got %d %s", rec.Code, rec.Body.String())
	}
	if rec := postAdmin(t, h, `{"name":"citext","enable":true}`, "not-the-key"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without the service role: want 401, got %d", rec.Code)
	}
}

func TestRLSHandlerTogglesAllowedSchemasOnly(t *testing.T) {
	pools := secsqlPools(t)
	h := RLSHandler(authorised(&config.Config{}), pools)
	pool, _ := pools()

	if rec := postAdmin(t, h, `{"schema":"auth","table":"users","enable":true}`, serviceKey); rec.Code != http.StatusOK {
		t.Fatalf("enable RLS: %d %s", rec.Code, rec.Body.String())
	}
	if !rowSecurity(t, pool, "auth.users") {
		t.Fatal("RLS was not enabled on auth.users")
	}
	if rec := postAdmin(t, h, `{"schema":"public","table":"anything","enable":true}`, serviceKey); rec.Code != http.StatusBadRequest {
		t.Fatalf("public schema: want 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPolicyHandlerCreatesAndDropsOnAllowedSchemas(t *testing.T) {
	pools := secsqlPools(t)
	h := PolicyHandler(authorised(&config.Config{}), pools)
	pool, _ := pools()
	defer func() {
		_ = postAdmin(t, h, `{"schema":"auth","table":"users","op":"drop","name":"sq2_test_pol"}`, serviceKey)
	}()

	create := `{"schema":"auth","table":"users","op":"create","name":"sq2_test_pol","command":"SELECT","roles":["authenticated"],"using":"true"}`
	if rec := postAdmin(t, h, create, serviceKey); rec.Code != http.StatusOK {
		t.Fatalf("create policy: %d %s", rec.Code, rec.Body.String())
	}
	if !policyExists(t, pool, "sq2_test_pol") {
		t.Fatal("policy was not created")
	}
	if rec := postAdmin(t, h, `{"schema":"auth","table":"users","op":"drop","name":"sq2_test_pol"}`, serviceKey); rec.Code != http.StatusOK {
		t.Fatalf("drop policy: %d %s", rec.Code, rec.Body.String())
	}
	if policyExists(t, pool, "sq2_test_pol") {
		t.Fatal("policy was not dropped")
	}
}

func extensionInstalled(t *testing.T, pool Pool, name string) bool {
	return scalarBool(t, pool, "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)", name)
}

func rowSecurity(t *testing.T, pool Pool, rel string) bool {
	return scalarBool(t, pool, "SELECT relrowsecurity FROM pg_class WHERE oid = $1::regclass", rel)
}

func policyExists(t *testing.T, pool Pool, name string) bool {
	return scalarBool(t, pool, "SELECT EXISTS (SELECT 1 FROM pg_policy WHERE polname = $1)", name)
}

func scalarBool(t *testing.T, pool Pool, sql string, arg string) bool {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var got bool
	if err := tx.QueryRow(context.Background(), sql, arg).Scan(&got); err != nil {
		t.Fatalf("query: %v", err)
	}
	return got
}
