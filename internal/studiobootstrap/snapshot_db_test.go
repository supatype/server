package studiobootstrap

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
)

// Everything Studio renders comes from the snapshot the last push wrote. What
// happens when it is missing, unreadable, or the database is not there decides
// whether Studio shows an empty project or says it cannot tell.

// snapshotStore builds a project database holding this AST, or skips.
//
// Run the DB-backed packages with -p 1: they share one database and the same
// `_supatype` table names.
func snapshotStore(t *testing.T, ast string) (context.Context, *data.Resources) {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the snapshot tests against Postgres")
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

	stmts := []string{
		`CREATE SCHEMA IF NOT EXISTS _supatype`,
		`DROP TABLE IF EXISTS _supatype.schema_state`,
		`CREATE TABLE _supatype.schema_state (
			id INT PRIMARY KEY,
			ast_snapshot JSONB,
			admin_config JSONB)`,
	}
	if ast != "" {
		stmts = append(stmts,
			`INSERT INTO _supatype.schema_state (id, ast_snapshot, admin_config)
			 VALUES (1, '`+ast+`'::jsonb, '{"theme":"dark"}'::jsonb)`)
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS _supatype.schema_state`)
		ResetIdentityScopeCache()
		ResetFieldScopeCache()
	})
	ResetIdentityScopeCache()
	ResetFieldScopeCache()
	return ctx, resources
}

// postAndSecretAST is an AST with one table anyone may read and one only its owner may.
const postAndSecretAST = `{"models":[
	{"name":"Post","annotations":{"db":{"tableName":"posts"},
	 "platform":{"access":{"read":{"type":"public"},"create":{"type":"authenticated"},
	   "fields":{"secret":{"read":{"type":"role","roles":["admin"]}},
	             "title":{"read":{"type":"public"}},
	             "owned":{"read":{"type":"owner","column":"author_id"}},
	             "writeonly":{"write":{"type":"private"}}}}}}},
	{"name":"Secret","annotations":{"platform":{"access":{"read":{"type":"private"},
	   "create":{"type":"private"},"update":{"type":"private"},"delete":{"type":"private"}}}}}
]}`

// ─── Loading ──────────────────────────────────────────────────────────────────

func TestLoadSnapshot(t *testing.T) {
	ctx, resources := snapshotStore(t, postAndSecretAST)

	snapshot, err := LoadSnapshot(ctx, resources)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.AST) == 0 {
		t.Error("no AST")
	}
	if len(snapshot.Hash) != 32 {
		t.Errorf("hash = %q, want 32 characters", snapshot.Hash)
	}
	if !strings.Contains(string(snapshot.AdminConfig), "dark") {
		t.Errorf("admin config = %s", snapshot.AdminConfig)
	}

	// The hash covers the content, so reading the same state twice gives the
	// same answer and a client's cache is not invalidated for nothing.
	again, err := LoadSnapshot(ctx, resources)
	if err != nil {
		t.Fatal(err)
	}
	if again.Hash != snapshot.Hash {
		t.Errorf("the hash changed between reads: %q then %q", snapshot.Hash, again.Hash)
	}
}

// A change to either half invalidates a client's cached copy.
func TestTheHashCoversBothHalves(t *testing.T) {
	ctx, resources := snapshotStore(t, postAndSecretAST)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}

	first, err := LoadSnapshot(ctx, resources)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE _supatype.schema_state SET admin_config = '{"theme":"light"}'::jsonb WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	second, err := LoadSnapshot(ctx, resources)
	if err != nil {
		t.Fatal(err)
	}
	if second.Hash == first.Hash {
		t.Error("changing the admin config did not change the hash")
	}
}

// A project that has never been pushed has nothing for Studio to render, and
// that is a distinct answer from a database it could not reach.
func TestASnapshotThatIsNotThere(t *testing.T) {
	ctx, resources := snapshotStore(t, "")

	if _, err := LoadSnapshot(ctx, resources); err != ErrNoSchemaState {
		t.Errorf("with no row: %v, want ErrNoSchemaState", err)
	}

	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO _supatype.schema_state (id, ast_snapshot) VALUES (1, NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(ctx, resources); err != ErrNoSchemaState {
		t.Errorf("with a null snapshot: %v, want ErrNoSchemaState", err)
	}
}

// A database that is not there, or a table that is not, is reported rather than
// answered with an empty schema.
func TestLoadSnapshotWithoutADatabase(t *testing.T) {
	if _, err := LoadSnapshot(context.Background(), nil); err == nil {
		t.Error("want an error with no resources")
	}

	ctx, resources := snapshotStore(t, postAndSecretAST)
	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE _supatype.schema_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(ctx, resources); err == nil {
		t.Error("want an error with no schema_state table")
	}
}

// ─── What a caller is shown ───────────────────────────────────────────────────

// A model every operation denies is omitted entirely. Sending it and hiding it
// in the browser would tell every caller what tables exist, which is what the
// access rules said they may not know.
func TestFilterForCallerOmitsWhatIsWhollyDenied(t *testing.T) {
	ctx, resources := snapshotStore(t, postAndSecretAST)
	snapshot, err := LoadSnapshot(ctx, resources)
	if err != nil {
		t.Fatal(err)
	}

	models, err := FilterForCaller(snapshot, Caller{UserID: "user-1", AppRole: "authenticated"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v, want only the reachable one", models)
	}
	if models[0].Name != "Post" || models[0].Table != "posts" {
		t.Errorf("model = %+v", models[0])
	}
	if models[0].Access["read"] != VerdictAllow || models[0].Access["create"] != VerdictAllow {
		t.Errorf("access = %v", models[0].Access)
	}
	// Every operation is answered, so Studio never has to guess at a missing key.
	for _, op := range []string{"read", "create", "update", "delete"} {
		if _, ok := models[0].Access[op]; !ok {
			t.Errorf("%s is missing from the access map", op)
		}
	}
	// An operation with no rule is denied, because deny-by-default is the premise.
	if models[0].Access["delete"] != VerdictDeny {
		t.Errorf("delete = %s, want deny", models[0].Access["delete"])
	}
}

// A model with no table name is addressed by its own name.
func TestAModelWithNoTableName(t *testing.T) {
	ctx, resources := snapshotStore(t, `{"models":[
		{"name":"Widget","annotations":{"platform":{"access":{"read":{"type":"public"}}}}}]}`)
	snapshot, err := LoadSnapshot(ctx, resources)
	if err != nil {
		t.Fatal(err)
	}

	models, err := FilterForCaller(snapshot, Caller{})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Table != "Widget" {
		t.Errorf("models = %+v", models)
	}
}

// An AST that will not parse is reported rather than rendered as a project with
// no tables.
func TestFilteringAnUnreadableAST(t *testing.T) {
	if _, err := FilterForCaller(&Snapshot{AST: json.RawMessage(`{"models":"not a list"}`)}, Caller{}); err == nil {
		t.Error("FilterForCaller: want an error")
	}
	if _, err := FieldVerdictsForCaller(&Snapshot{AST: json.RawMessage(`{"models":42}`)}, Caller{}); err == nil {
		t.Error("FieldVerdictsForCaller: want an error")
	}
	if _, err := maskedFieldsFromAST([]byte(`{"models":true}`)); err == nil {
		t.Error("maskedFieldsFromAST: want an error")
	}
}

// ─── Field verdicts ───────────────────────────────────────────────────────────

// Studio needs to know per column before it draws a form: a masked value must
// not be posted back as an empty string, and a column nobody may write should
// be disabled rather than silently rejected on save.
func TestFieldVerdictsForCaller(t *testing.T) {
	ctx, resources := snapshotStore(t, postAndSecretAST)
	snapshot, err := LoadSnapshot(ctx, resources)
	if err != nil {
		t.Fatal(err)
	}

	verdicts, err := FieldVerdictsForCaller(snapshot, Caller{UserID: "user-1", AppRole: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	fields, ok := verdicts["posts"]
	if !ok {
		t.Fatalf("verdicts = %+v, want the table with fields", verdicts)
	}
	for _, column := range []string{"secret", "title", "owned", "writeonly"} {
		if _, present := fields[column]; !present {
			t.Errorf("%s is missing", column)
		}
	}

	// A model that declares no field rules is not in the map at all: there is
	// nothing per column to say about it.
	if _, present := verdicts["Secret"]; present {
		t.Error("a model with no field rules was included")
	}
}

// ─── The memoised classifications ─────────────────────────────────────────────

// The identity-scope classification decides whether the REST cache may share one
// entry between callers, so it has to be right about which tables vary by caller.
func TestIdentityScopedTables(t *testing.T) {
	ctx, resources := snapshotStore(t, `{"models":[
		{"name":"Post","annotations":{"db":{"tableName":"posts"},
		 "platform":{"access":{"read":{"type":"public"}}}}},
		{"name":"Mine","annotations":{"db":{"tableName":"mine"},
		 "platform":{"access":{"read":{"type":"owner","column":"author_id"}}}}},
		{"name":"Unnamed","annotations":{"platform":{"access":{"read":{"type":"public"}}}}}
	]}`)

	tables, ok := IdentityScopedTables(ctx, resources)
	if !ok {
		t.Fatal("the classification could not be read")
	}
	if tables["posts"] {
		t.Error("a public table was marked identity-scoped")
	}
	if !tables["mine"] {
		t.Error("an owner-scoped table was not marked identity-scoped")
	}
	if _, present := tables["Unnamed"]; !present {
		t.Error("a model with no table name was not keyed by its own name")
	}

	// The second call is memoised, so the request path does not pay for it.
	if _, ok := IdentityScopedTables(ctx, resources); !ok {
		t.Error("the memoised answer was lost")
	}
}

// "We could not check" is not a reason to start sharing responses between
// users, so an unreachable classification is a refusal rather than an empty map.
func TestIdentityScopedTablesWithNothingToRead(t *testing.T) {
	ctx, resources := snapshotStore(t, "")

	if tables, ok := IdentityScopedTables(ctx, resources); ok || tables != nil {
		t.Errorf("got (%v, %v), want a refusal", tables, ok)
	}
}

// A blip after a successful read keeps serving the previous answer rather than
// flapping to "unknown"; the TTL still bounds how old it can get.
func TestIdentityScopedTablesKeepsThePreviousAnswer(t *testing.T) {
	ctx, resources := snapshotStore(t, postAndSecretAST)

	if _, ok := IdentityScopedTables(ctx, resources); !ok {
		t.Fatal("the first read failed")
	}

	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE _supatype.schema_state`); err != nil {
		t.Fatal(err)
	}
	expireIdentityScope()

	if _, ok := IdentityScopedTables(ctx, resources); !ok {
		t.Error("a transient failure lost the previous answer")
	}
}

// An AST that will not parse is a refusal: the classification is a safety
// decision and a guess is not one.
func TestIdentityScopedTablesWithAnUnreadableAST(t *testing.T) {
	ctx, resources := snapshotStore(t, `{"models":"not a list"}`)

	if _, ok := IdentityScopedTables(ctx, resources); ok {
		t.Error("an unreadable AST was classified")
	}
}

// The masked-field classification says which columns a null might be hiding.
func TestMaskedFields(t *testing.T) {
	ctx, resources := snapshotStore(t, postAndSecretAST)

	tables, ok := MaskedFields(ctx, resources)
	if !ok {
		t.Fatal("the classification could not be read")
	}
	masks := tables["posts"]
	if len(masks) != 2 {
		t.Fatalf("masks = %+v, want the two restricted columns", masks)
	}
	// Sorted, so the header value is stable between requests.
	if masks[0].Column != "owned" || masks[1].Column != "secret" {
		t.Errorf("masks = %+v, want them sorted", masks)
	}
	if !masks[0].RowDependent {
		t.Error("an owner rule is row-dependent")
	}
	if masks[1].RowDependent {
		t.Error("a role rule is the same for every row")
	}

	// Memoised.
	if _, ok := MaskedFields(ctx, resources); !ok {
		t.Error("the memoised answer was lost")
	}
}

func TestMaskedFieldsWithNothingToRead(t *testing.T) {
	ctx, resources := snapshotStore(t, "")

	if tables, ok := MaskedFields(ctx, resources); ok || tables != nil {
		t.Errorf("got (%v, %v), want a refusal", tables, ok)
	}
}

func TestMaskedFieldsKeepsThePreviousAnswer(t *testing.T) {
	ctx, resources := snapshotStore(t, postAndSecretAST)

	if _, ok := MaskedFields(ctx, resources); !ok {
		t.Fatal("the first read failed")
	}

	pool, err := resources.AdminPool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE _supatype.schema_state`); err != nil {
		t.Fatal(err)
	}
	expireFieldScope()

	if _, ok := MaskedFields(ctx, resources); !ok {
		t.Error("a transient failure lost the previous answer")
	}
}

func TestMaskedFieldsWithAnUnreadableAST(t *testing.T) {
	ctx, resources := snapshotStore(t, `{"models":"not a list"}`)

	if _, ok := MaskedFields(ctx, resources); ok {
		t.Error("an unreadable AST was classified")
	}
}
