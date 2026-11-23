package sqlrunner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/supatype/server/internal/config"
)

const (
	editorTestOwner    = "sqlrunner_test_owner"
	editorTestDatabase = "sqlrunner_editor_test"
	editorRole         = "supatype_sql_editor"
	// adoptEditorEnv lets the tests use a cluster that already has the editor
	// role, as the supatype/postgres image does. They reset its password and
	// drop what they make, so it must be a scratch container.
	adoptEditorEnv = "SUPATYPE_TEST_ADOPT_EDITOR"
)

// An object the SQL editor creates belongs to the database owner once the
// statement commits, not to the editor's login role, through the handover
// function the stack's start-up creates.
//
// The editor is a member of the owner WITH SET FALSE, as the postgres image
// makes it, so a REASSIGN run in the editor's own session is refused.
func TestEditorObjectsAreHandedToTheDatabaseOwner(t *testing.T) {
	stack := editorStack(t)
	installHandover(t, stack)

	rec := post(t, authorised(&config.Config{}), serving(stack.editor),
		`{"query":"CREATE TABLE public.editor_made (id int)"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	if owner := tableOwner(t, stack, "editor_made"); owner != stack.owner {
		t.Errorf("editor_made is owned by %q, want the database owner %q", owner, stack.owner)
	}
}

// A stack whose start-up has not created the function yet keeps today's inline
// REASSIGN. Where the editor may not SET ROLE to the owner that is refused, and
// the statement must not commit: this is the failure the function exists to end.
func TestWithoutTheHandoverFunctionAnEditorThatCannotSetRoleIsRefused(t *testing.T) {
	stack := editorStack(t)

	rec := post(t, authorised(&config.Config{}), serving(stack.editor),
		`{"query":"CREATE TABLE public.editor_refused (id int)"}`, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "must be able to SET ROLE") {
		t.Errorf("error = %s, want the SET ROLE refusal", rec.Body.String())
	}
	var exists bool
	if err := stack.admin.QueryRow(context.Background(),
		`SELECT to_regclass('public.editor_refused') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("look up table: %v", err)
	}
	if exists {
		t.Error("the statement committed although the handover failed")
	}
}

// A deployment that has not configured the editor DSN runs the editor on the
// SQL DSN, often on a cluster with no editor role at all. The handover must not
// turn every statement there into a failure.
func TestTheFallbackConnectionStillRuns(t *testing.T) {
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the SQL editor against Postgres")
	}
	root := connect(t, dsn, func(*pgxpool.Config) {})
	rec := post(t, authorised(&config.Config{}), func() (Pool, error) { return root, nil }, `{"query":"select 1 as n"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
}

// On the supatype postgres image the editor is a member of the owner WITH SET
// FALSE, so it cannot run REASSIGN OWNED itself on Postgres 16 and later. What it
// creates still ends up the owner's, through the image's reassign function, and
// it still cannot SET ROLE into the owner.
//
// Needs the image's DSN as a superuser (SUPATYPE_SECSQL_DSN) and the password the
// image was started with for supatype_sql_editor (SUPATYPE_SECSQL_EDITOR_PASSWORD).
func TestTheImageEditorHandsObjectsToTheOwner(t *testing.T) {
	admin, editor := imageEditorPools(t)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = admin.Exec(ctx, `DROP TABLE IF EXISTS public.sqlrunner_image_editor_made`) })

	rec := post(t, authorised(&config.Config{}), func() (Pool, error) { return editor, nil },
		`{"query":"CREATE TABLE public.sqlrunner_image_editor_made (id serial PRIMARY KEY)"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	var owner, dbOwner string
	err := admin.QueryRow(ctx, `SELECT pg_catalog.pg_get_userbyid(c.relowner),
	    (SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_catalog.pg_database WHERE datname = pg_catalog.current_database())
	  FROM pg_catalog.pg_class c WHERE c.oid = 'public.sqlrunner_image_editor_made'::regclass`).Scan(&owner, &dbOwner)
	if err != nil {
		t.Fatalf("read owner: %v", err)
	}
	if owner != dbOwner {
		t.Errorf("the table is owned by %q, want the database owner %q", owner, dbOwner)
	}

	rec = post(t, authorised(&config.Config{}), func() (Pool, error) { return editor, nil },
		`{"query":"SET ROLE `+dbOwner+`"}`, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("SET ROLE %s: status = %d (%s), want 422", dbOwner, rec.Code, rec.Body.String())
	}
}

// imageEditorPools returns a superuser pool and an editor pool on the image's
// database, or skips when either credential is missing.
func imageEditorPools(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_SECSQL_DSN")
	password := os.Getenv("SUPATYPE_SECSQL_EDITOR_PASSWORD")
	if dsn == "" || password == "" {
		t.Skip("set SUPATYPE_SECSQL_DSN and SUPATYPE_SECSQL_EDITOR_PASSWORD to run the editor against the postgres image")
	}
	admin := connect(t, dsn, func(*pgxpool.Config) {})
	editor := connect(t, dsn, func(c *pgxpool.Config) {
		c.ConnConfig.User = editorRole
		c.ConnConfig.Password = password
	})
	return admin, editor
}

// editorEnv is a database and the roles around it: a superuser pool on the
// database, a pool connected as the editor, and the role that owns the database.
type editorEnv struct {
	admin  *pgxpool.Pool
	editor *pgxpool.Pool
	owner  string
}

// editorStack provisions a database owned by a non-superuser role and an editor
// login that is a member of it WITH INHERIT TRUE, SET FALSE, or skips when there
// is no database to use.
func editorStack(t *testing.T) editorEnv {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the SQL editor against Postgres")
	}
	root := connect(t, dsn, func(*pgxpool.Config) {})
	database, owner := provisionEditor(t, root)
	admin := connect(t, dsn, func(c *pgxpool.Config) { c.ConnConfig.Database = database })
	editor := connect(t, dsn, func(c *pgxpool.Config) {
		c.ConnConfig.Database = database
		c.ConnConfig.User = editorRole
		c.ConnConfig.Password = "editor"
	})
	return editorEnv{admin: admin, editor: editor, owner: owner}
}

// provisionEditor makes the owner, the editor and the database, and removes
// them when the test ends. It answers the database and the owner to use.
//
// A cluster that already has an editor role belongs to someone else, so the
// test leaves it alone unless adoptEditorEnv says it is a scratch one. Then it
// makes a database of its own, owned by the role that owns the DSN's.
func provisionEditor(t *testing.T, root *pgxpool.Pool) (database, owner string) {
	t.Helper()
	ctx := context.Background()
	var exists bool
	if err := root.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, editorRole).Scan(&exists); err != nil {
		t.Fatalf("look up %s: %v", editorRole, err)
	}
	if exists {
		return adoptEditor(t, root)
	}
	t.Cleanup(func() { teardownEditor(root) })
	for _, stmt := range []string{
		`CREATE ROLE ` + editorTestOwner + ` NOLOGIN NOSUPERUSER`,
		`CREATE ROLE ` + editorRole + ` LOGIN NOSUPERUSER PASSWORD 'editor'`,
		`GRANT ` + editorTestOwner + ` TO ` + editorRole + ` WITH INHERIT TRUE, SET FALSE`,
		`CREATE DATABASE ` + editorTestDatabase + ` OWNER ` + editorTestOwner,
	} {
		if _, err := root.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return editorTestDatabase, editorTestOwner
}

// adoptEditor uses the editor role a cluster already has, as the postgres image
// provisions it, in a database of its own.
//
// The image keeps its handover function in the database the DSN names, and the
// tests that assume no function exists would otherwise find it, or drop it on
// their way out. A throwaway database owned by the same role has none of the
// image's objects, and nothing the tests make or drop in it touches the shared
// one. The roles are cluster wide, so the tests must not drop any they did not
// make.
func adoptEditor(t *testing.T, root *pgxpool.Pool) (database, owner string) {
	t.Helper()
	if os.Getenv(adoptEditorEnv) != "1" {
		t.Skipf("%s already exists on this cluster; run against a scratch Postgres, or set %s=1 for one", editorRole, adoptEditorEnv)
	}
	ctx := context.Background()
	if err := root.QueryRow(ctx,
		`SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = current_database()`).Scan(&owner); err != nil {
		t.Fatalf("read the database owner: %v", err)
	}
	if _, err := root.Exec(ctx, `ALTER ROLE `+editorRole+` WITH LOGIN PASSWORD 'editor'`); err != nil {
		t.Fatalf("set the editor password: %v", err)
	}
	database = scratchDatabaseName(t)
	if _, err := root.Exec(ctx, `CREATE DATABASE `+database+` OWNER `+owner); err != nil {
		t.Fatalf("create %s: %v", database, err)
	}
	t.Cleanup(func() { _, _ = root.Exec(ctx, `DROP DATABASE IF EXISTS `+database+` WITH (FORCE)`) })
	return database, owner
}

// scratchDatabaseName is a database name no other test run shares.
func scratchDatabaseName(t *testing.T) string {
	t.Helper()
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("name a scratch database: %v", err)
	}
	return editorTestDatabase + "_" + hex.EncodeToString(suffix)
}

// teardownEditor runs after every pool on the test database has been closed,
// because t.Cleanup runs in reverse and the pools registered theirs later.
func teardownEditor(root *pgxpool.Pool) {
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP DATABASE IF EXISTS ` + editorTestDatabase + ` WITH (FORCE)`,
		`DROP ROLE IF EXISTS ` + editorRole,
		`DROP ROLE IF EXISTS ` + editorTestOwner,
	} {
		_, _ = root.Exec(ctx, stmt)
	}
}

// tableOwner is who owns public.<name>, read as the superuser.
func tableOwner(t *testing.T, stack editorEnv, name string) string {
	t.Helper()
	var owner string
	err := stack.admin.QueryRow(context.Background(),
		`SELECT tableowner FROM pg_catalog.pg_tables WHERE schemaname = 'public' AND tablename = $1`, name).Scan(&owner)
	if err != nil {
		t.Fatalf("read owner of %s: %v", name, err)
	}
	return owner
}

// connect opens a pool on dsn after applying adjust, closed when the test ends.
func connect(t *testing.T, dsn string, adjust func(*pgxpool.Config)) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	adjust(cfg)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
