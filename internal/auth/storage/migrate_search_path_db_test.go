package storage

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gobuffalo/pop/v6"
	"github.com/jackc/pgx/v5"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// The migrate command once wrote `ALTER DATABASE ... SET search_path TO auth, public,
// extensions`. A database default reaches every connection, not only the server's, so on a
// local Postgres with no role-level path (native dev as `postgres`) a plain `create table x`
// through the project's DATABASE_URL landed in auth, and `supatype pull` never saw it. The
// server's own connections carry `search_path=auth` in their URL and need no database default;
// every migrate takes back the default an older server wrote.

// scratchDatabase creates a database of its own on SUPATYPE_TEST_DSN, because these tests
// change a database-level setting, which every other test connecting there would inherit.
func scratchDatabase(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN (a superuser) to run the search_path tests against Postgres")
	}
	name := fmt.Sprintf("st_search_path_%d", time.Now().UnixNano())
	adminExec(t, dsn, "CREATE DATABASE "+name)
	t.Cleanup(func() { adminExec(t, dsn, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	return withURL(t, dsn, func(u *url.URL) { u.Path = "/" + name })
}

func withURL(t *testing.T, dsn string, edit func(*url.URL)) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	edit(u)
	return u.String()
}

func adminExec(t *testing.T, dsn, sql string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// databaseDefault is the database-level search_path entry, or "" when there is none. It is
// read from the catalog, so a role-level path on the connecting role cannot hide it.
func databaseDefault(t *testing.T, dsn string) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var out string
	err = conn.QueryRow(ctx, `SELECT coalesce(max(c), '')
		  FROM pg_catalog.pg_db_role_setting s, unnest(s.setconfig) AS c
		 WHERE s.setrole = 0 AND c LIKE 'search_path=%'
		   AND s.setdatabase = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())`).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// seedDefault writes a database-level search_path given as SQL.
func seedDefault(t *testing.T, dsn, pathSQL string) {
	t.Helper()
	adminExec(t, dsn, fmt.Sprintf(
		`DO $$ BEGIN EXECUTE format('ALTER DATABASE %%I SET search_path TO %s', current_database()); END $$`,
		pathSQL))
}

// seedOldServerDefault writes the path exactly as the old migrate did, quoting every name.
func seedOldServerDefault(t *testing.T, dsn, ns string, withExtensions bool) {
	t.Helper()
	path := pq.QuoteIdentifier(ns) + `, "public"`
	if withExtensions {
		path += `, "extensions"`
	}
	seedDefault(t, dsn, path)
}

// migrationConn opens the connection the migrate command opens, from the same details.
func migrationConn(t *testing.T, dsn, ns string) *pop.Connection {
	t.Helper()
	deets, err := MigrationConnectionDetails(dsn, "", ns, "auth_migrations")
	if err != nil {
		t.Fatal(err)
	}
	return openConn(t, deets)
}

func openConn(t *testing.T, deets *pop.ConnectionDetails) *pop.Connection {
	t.Helper()
	db, err := pop.NewConnection(deets)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// The URL alone gives every migration connection the auth path, so no SET is needed.
func TestMigrationConnectionStartsInTheNamespace(t *testing.T) {
	db := migrationConn(t, scratchDatabase(t), "auth")
	var session string
	if err := db.RawQuery("SHOW search_path").First(&session); err != nil {
		t.Fatal(err)
	}
	if session != "auth" {
		t.Errorf("migration connection search_path = %q, want auth before any SET", session)
	}
	if err := CheckMigrationSearchPath(db, "auth"); err != nil {
		t.Errorf("check refused a connection that starts in auth: %v", err)
	}
}

// strippedConn is a migrate connection whose search_path never reached Postgres, as behind a
// pooler that drops startup parameters (PgBouncer's ignore_startup_parameters).
func strippedConn(t *testing.T, dsn string) *pop.Connection {
	t.Helper()
	deets, err := MigrationConnectionDetails(dsn, "", "auth", "auth_migrations")
	if err != nil {
		t.Fatal(err)
	}
	deets.URL = withURL(t, deets.URL, func(u *url.URL) {
		q := u.Query()
		q.Del("search_path")
		u.RawQuery = q.Encode()
	})
	return openConn(t, deets)
}

// The connection then sits on the database's own path. Even one that starts with auth is not
// what the URL asked for, so only the exact value passes.
func TestCheckMigrationSearchPathRefusesAStrippedPath(t *testing.T) {
	for _, dbDefault := range []string{"", "auth, extensions"} {
		dsn := scratchDatabase(t)
		if dbDefault != "" {
			seedDefault(t, dsn, dbDefault)
		}
		err := CheckMigrationSearchPath(strippedConn(t, dsn), "auth")
		if err == nil || !strings.Contains(err.Error(), "ignore_startup_parameters") {
			t.Errorf("database default %q: want an error naming the stripped parameter, got %v", dbDefault, err)
		}
	}
}

// Behind such a pooler the old default must still be taken back, on the same run that then
// stops on the check.
func TestSettleResetsTheOldDefaultBeforeRefusingAStrippedPath(t *testing.T) {
	dsn := scratchDatabase(t)
	seedOldServerDefault(t, dsn, "auth", true)
	err := SettleMigrationSearchPath(strippedConn(t, dsn), "auth")
	if err == nil || !strings.Contains(err.Error(), "ignore_startup_parameters") {
		t.Errorf("want the stripped path refused, got %v", err)
	}
	if got := databaseDefault(t, dsn); got != "" {
		t.Errorf("database-level default = %q, want it reset before the refusal", got)
	}
}

// user is a keyword, so Postgres stores it quoted in the list; the match must quote it too.
func TestResetTakesBackTheDefaultAnOldServerWrote(t *testing.T) {
	cases := []struct {
		ns   string
		full bool
	}{{"auth", true}, {"auth", false}, {"user", true}}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/extensions=%v", c.ns, c.full), func(t *testing.T) {
			dsn := scratchDatabase(t)
			seedOldServerDefault(t, dsn, c.ns, c.full)
			if err := SettleMigrationSearchPath(migrationConn(t, dsn, c.ns), c.ns); err != nil {
				t.Fatal(err)
			}
			if got := databaseDefault(t, dsn); got != "" {
				t.Errorf("database-level default = %q, want it reset", got)
			}
		})
	}
}

func TestResetKeepsAnOperatorsDefault(t *testing.T) {
	dsn := scratchDatabase(t)
	seedDefault(t, dsn, "public, extensions")
	if err := ResetLegacyDatabaseSearchPath(migrationConn(t, dsn, "auth"), "auth"); err != nil {
		t.Fatal(err)
	}
	if got := databaseDefault(t, dsn); got != "search_path=public, extensions" {
		t.Errorf("database-level default = %q, want the operator's own left alone", got)
	}
}

// A role that does not own the database cannot reset it. Migrating must go on, and the
// warning must name the value found and the statement to run.
func TestResetWarnsAndCarriesOnForANonOwner(t *testing.T) {
	dsn := scratchDatabase(t)
	seedOldServerDefault(t, dsn, "auth", true)
	role := fmt.Sprintf("st_nonowner_%d", time.Now().UnixNano())
	adminExec(t, dsn, "CREATE ROLE "+role+" LOGIN PASSWORD 'pw'")
	t.Cleanup(func() { adminExec(t, dsn, "DROP ROLE IF EXISTS "+role) })
	asRole := withURL(t, dsn, func(u *url.URL) { u.User = url.UserPassword(role, "pw") })
	db := migrationConn(t, asRole, "auth")
	hook := logtest.NewGlobal()
	defer hook.Reset()
	if err := ResetLegacyDatabaseSearchPath(db, "auth"); err != nil {
		t.Fatalf("want the migrate to carry on, got %v", err)
	}
	entry := hook.LastEntry()
	if entry == nil || entry.Level != logrus.WarnLevel ||
		!strings.Contains(entry.Message, "search_path=auth, public, extensions") ||
		!strings.Contains(entry.Message, "RESET search_path") {
		t.Errorf("want a warning naming the old value and ALTER DATABASE ... RESET search_path, got %+v", entry)
	}
	if got := databaseDefault(t, dsn); got == "" {
		t.Error("a non-owner reset the database default; the test proves nothing")
	}
}
