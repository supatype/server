package sqlrunner

import (
	"context"
	"errors"
	"fmt"
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
	if rec := postAdmin(t, h, `{"name":"citext","enable":false}`, serviceKey); rec.Code != http.StatusOK {
		t.Fatalf("disable citext: %d %s", rec.Code, rec.Body.String())
	}
	if extensionInstalled(t, pool, "citext") {
		t.Fatal("citext is still installed after disabling it")
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

// ─── Admin writes without a database ──────────────────────────────────────────

// adminHandlers is every SQ2 route, by name, over the given pools.
func adminHandlers(pools Pools) map[string]http.Handler {
	cfg := authorised(&config.Config{})
	return map[string]http.Handler{
		"extensions": ExtensionHandler(cfg, pools),
		"rls":        RLSHandler(cfg, pools),
		"policies":   PolicyHandler(cfg, pools),
	}
}

func TestAdminWritesArePOSTOnly(t *testing.T) {
	noPool := func() (Pool, error) { t.Fatal("must not reach the database"); return nil, nil }
	for name, h := range adminHandlers(noPool) {
		req := httptest.NewRequest(http.MethodGet, "/studio-admin", nil)
		req.Header.Set("Authorization", "Bearer "+serviceKey)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: GET got %d, want 405", name, rec.Code)
		}
	}
}

func TestAdminWritesReportAnUnreachableDatabase(t *testing.T) {
	boom := errors.New("boom")
	for poolName, tc := range map[string]struct {
		pools Pools
		says  string
	}{
		"no database configured":                {func() (Pool, error) { return nil, boom }, "database not available"},
		"the pool will not begin a transaction": {serving(&fakePool{beginErr: boom}), "failed to begin transaction"},
	} {
		for name, h := range adminHandlers(tc.pools) {
			rec := postAdmin(t, h, `{}`, serviceKey)
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(errorBody(t, rec), tc.says) {
				t.Errorf("%s, %s: got %d %s, want 503 saying %q", poolName, name, rec.Code, rec.Body.String(), tc.says)
			}
		}
	}
}

// A body the route cannot use is the caller's to fix: 400, and the transaction
// it opened is rolled back with nothing run and nothing committed.
func TestAdminWritesRefuseBodiesTheyCannotUse(t *testing.T) {
	for _, tc := range []struct{ route, body, says string }{
		{"extensions", `not json`, "invalid request body"},
		{"extensions", `{"name":"","enable":true}`, "an extension name is required"},
		{"rls", `not json`, "invalid request body"},
		{"rls", `{"schema":"public","table":"t","enable":true}`, "is not the auth or storage schema"},
		{"policies", `not json`, "invalid request body"},
		{"policies", `{"schema":"pg_catalog","table":"t","op":"create","name":"p"}`, "is not the auth or storage schema"},
	} {
		tx := &fakeTx{}
		rec := postAdmin(t, adminHandlers(serving(&fakePool{tx: tx}))[tc.route], tc.body, serviceKey)
		if rec.Code != http.StatusBadRequest || !strings.Contains(errorBody(t, rec), tc.says) {
			t.Errorf("%s %s: got %d %s, want 400 saying %q", tc.route, tc.body, rec.Code, rec.Body.String(), tc.says)
		}
		if len(tx.calls) != 0 || !tx.rolledBack {
			t.Errorf("%s %s: ran %v, rolled back %t; want nothing run and a rollback", tc.route, tc.body, tx.calls, tx.rolledBack)
		}
	}
}

// A lookup that fails is not mistaken for an extension the image lacks.
func TestAFailedExtensionLookupIsNotAnUnknownExtension(t *testing.T) {
	tx := &fakeTx{queryRowErr: errors.New("connection reset")}
	rec := postAdmin(t, adminHandlers(serving(&fakePool{tx: tx}))["extensions"], `{"name":"citext","enable":true}`, serviceKey)
	if rec.Code != http.StatusInternalServerError || errorBody(t, rec) != "connection reset" {
		t.Fatalf("got %d %s, want 500 with the lookup's error", rec.Code, rec.Body.String())
	}
	if len(tx.execs) != 0 || tx.committed {
		t.Fatalf("ran %v and committed %t after a failed lookup", tx.execs, tx.committed)
	}
}

// The RLS and policy writes go through the schema's own helper with every
// caller value bound, and a commit that does not land is a failure.
func TestAdminWritesCallTheSchemasHelperAndReportACommitThatDoesNotLand(t *testing.T) {
	for _, tc := range []struct {
		route, body, sql, args string
	}{
		{
			"rls", `{"schema":"storage","table":"objects","enable":false}`,
			`SELECT "storage".supatype_studio_set_rls($1, $2)`, "[objects false]",
		},
		{
			"policies", `{"schema":"auth","table":"users","op":"create","name":"p","command":"SELECT","roles":["anon"],"using":"true"}`,
			`SELECT "auth".supatype_studio_write_policy($1, $2, $3, $4, $5, $6, $7)`, "[create users p SELECT [anon] true <nil>]",
		},
	} {
		tx := &fakeTx{commitErr: errors.New("boom")}
		rec := postAdmin(t, adminHandlers(serving(&fakePool{tx: tx}))[tc.route], tc.body, serviceKey)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(errorBody(t, rec), "failed to commit") {
			t.Errorf("%s: got %d %s, want 500 saying failed to commit", tc.route, rec.Code, rec.Body.String())
		}
		if len(tx.execs) != 1 || tx.execs[0].sql != tc.sql {
			t.Fatalf("%s: ran %v, want %s", tc.route, tx.execs, tc.sql)
		}
		if got := fmt.Sprint(derefArgs(tx.execs[0].args)); got != tc.args {
			t.Errorf("%s: bound %s, want %s", tc.route, got, tc.args)
		}
	}
}

// derefArgs prints the optional policy expressions by value.
func derefArgs(args []any) []any {
	out := make([]any, len(args))
	for i, arg := range args {
		if s, ok := arg.(*string); ok && s != nil {
			out[i] = *s
		} else if ok {
			out[i] = nil
		} else {
			out[i] = arg
		}
	}
	return out
}
