package sqlrunner

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/supatype/server/internal/config"
)

const (
	editorTestOwner    = "sqlrunner_test_owner"
	editorTestDatabase = "sqlrunner_editor_test"
	editorRole         = "supatype_sql_editor"
)

// An object the SQL editor creates belongs to the database owner once the
// statement commits, not to the editor's login role.
//
// The database here is owned by a non-superuser role, and the editor logs in as
// a plain member of it with no reassign function, so this is the plain REASSIGN
// an image from before public.supatype_sql_editor_reassign_owned() gets. The
// image's own shape is TestTheImageEditorHandsObjectsToTheOwner.
func TestEditorObjectsAreHandedToTheDatabaseOwner(t *testing.T) {
	admin, editor := editorPools(t)

	rec := post(t, authorised(&config.Config{}), func() (Pool, error) { return editor, nil },
		`{"query":"CREATE TABLE public.editor_made (id int)"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	var owner string
	err := admin.QueryRow(context.Background(),
		`SELECT tableowner FROM pg_catalog.pg_tables WHERE schemaname = 'public' AND tablename = 'editor_made'`).Scan(&owner)
	if err != nil {
		t.Fatalf("read owner: %v", err)
	}
	if owner != editorTestOwner {
		t.Errorf("editor_made is owned by %q, want the database owner %q", owner, editorTestOwner)
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

// editorPools provisions a database owned by a non-superuser role and an editor
// login that is a member of it. It returns a superuser pool on that database and
// a pool connected as the editor, or skips when there is no database to use.
func editorPools(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the SQL editor against Postgres")
	}
	root := connect(t, dsn, func(*pgxpool.Config) {})
	provisionEditor(t, root)
	admin := connect(t, dsn, func(c *pgxpool.Config) { c.ConnConfig.Database = editorTestDatabase })
	editor := connect(t, dsn, func(c *pgxpool.Config) {
		c.ConnConfig.Database = editorTestDatabase
		c.ConnConfig.User = editorRole
		c.ConnConfig.Password = "editor"
	})
	return admin, editor
}

// provisionEditor creates the owner, the editor and the database, and removes
// them when the test ends. A cluster that already has an editor role belongs to
// someone else, so the test leaves it alone rather than reset its password.
func provisionEditor(t *testing.T, root *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var exists bool
	if err := root.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, editorRole).Scan(&exists); err != nil {
		t.Fatalf("look up %s: %v", editorRole, err)
	}
	if exists {
		t.Skipf("%s already exists on this cluster; run against a scratch Postgres", editorRole)
	}
	t.Cleanup(func() { teardownEditor(root) })
	for _, stmt := range []string{
		`CREATE ROLE ` + editorTestOwner + ` NOLOGIN NOSUPERUSER`,
		`CREATE ROLE ` + editorRole + ` LOGIN NOSUPERUSER PASSWORD 'editor'`,
		`GRANT ` + editorTestOwner + ` TO ` + editorRole,
		`CREATE DATABASE ` + editorTestDatabase + ` OWNER ` + editorTestOwner,
	} {
		if _, err := root.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
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
