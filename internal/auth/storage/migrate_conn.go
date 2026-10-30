package storage

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/gobuffalo/pop/v6"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// MigrationConnectionDetails builds a connection for the auth schema's own maintenance: the
// migrate command and the index worker. The namespace goes into the URL as search_path, which
// pgx sends as a startup parameter, so every pooled connection starts in it and nothing has
// to SET it. The namespace is the configured one, already trimmed and validated at load.
//
// The pool is not pinned to one connection. Pop runs each migration file in its own
// transaction, possibly on a different connection, so a migration must not rely on session
// state left by an earlier file. Anything session-scoped a migration needs, it sets itself
// with SET LOCAL.
func MigrationConnectionDetails(dbURL, driver, ns, applicationName string) (*pop.ConnectionDetails, error) {
	withPath, err := EnsurePostgresSearchPathInURL(dbURL, ns)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(withPath)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("application_name", applicationName)
	u.RawQuery = q.Encode()
	if driver == "" {
		driver = u.Scheme
	}
	return &pop.ConnectionDetails{
		Dialect: driver,
		URL:     u.String(),
		Options: map[string]string{"migration_table_name": "schema_migrations", "Namespace": ns},
	}, nil
}

// CheckMigrationSearchPath refuses a migrate connection whose search_path is not exactly the
// one its URL sent. A pooler that drops startup parameters would leave the database's own
// path in place, and Pop's unqualified schema_migrations would land somewhere other than auth.
func CheckMigrationSearchPath(db *pop.Connection, ns string) error {
	if ns == "" {
		return nil
	}
	var path string
	if err := db.RawQuery("SHOW search_path").First(&path); err != nil {
		return fmt.Errorf("reading the migrate connection's search_path: %w", err)
	}
	if path == ns {
		return nil
	}
	return fmt.Errorf("the migrate connection's search_path is %q, not the %q its database URL "+
		"asked for: the startup parameter did not reach Postgres. A pooler between them may drop "+
		"startup parameters (PgBouncer ignore_startup_parameters); migrate against Postgres directly", path, ns)
}

// legacySearchPathQuery returns the current database's name and its database-level
// search_path entry when that entry is exactly one an older migrate wrote (`<ns>, public,
// extensions`, or `<ns>, public` where extensions was refused), and no row otherwise, so a
// path an operator set is not matched. Postgres stores each name of the list as quote_ident
// renders it (a keyword such as user comes back quoted), so the comparison quotes the same way.
const legacySearchPathQuery = `SELECT d.datname::text AS datname, c AS setting
  FROM pg_catalog.pg_database d
  JOIN pg_catalog.pg_db_role_setting s ON s.setdatabase = d.oid AND s.setrole = 0
 CROSS JOIN LATERAL unnest(s.setconfig) AS c
 WHERE d.datname = current_database()
   AND c IN (format('search_path=%s, public, extensions', quote_ident(?)),
             format('search_path=%s, public', quote_ident(?)))`

type legacySearchPath struct {
	Database string `db:"datname"`
	Setting  string `db:"setting"`
}

// errLegacySearchPathLookup and errLegacySearchPathReset say which step failed.
var (
	errLegacySearchPathLookup = errors.New("looking up the database-level search_path an older migrate wrote")
	errLegacySearchPathReset  = errors.New("resetting the database-level search_path an older migrate wrote")
)

// ResetLegacyDatabaseSearchPath takes back the database-level search_path an older migrate
// wrote. It runs on every migrate, so a database an older binary altered again is healed too.
// A role that does not own the database cannot reset it: that is logged with the value found
// and the statement to run, and migrating carries on.
func ResetLegacyDatabaseSearchPath(db *pop.Connection, ns string) error {
	if ns == "" {
		return nil
	}
	var found []legacySearchPath
	if err := db.RawQuery(legacySearchPathQuery, ns, ns).All(&found); err != nil {
		return fmt.Errorf("%w: %w", errLegacySearchPathLookup, err)
	}
	if len(found) == 0 {
		return nil
	}
	return resetDatabaseSearchPath(db, found[0])
}

func resetDatabaseSearchPath(db *pop.Connection, old legacySearchPath) error {
	stmt := "ALTER DATABASE " + pq.QuoteIdentifier(old.Database) + " RESET search_path"
	err := db.RawQuery(stmt).Exec()
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42501" {
		logrus.Warnf("database %q still has the database-level %s an older migrate wrote, and this "+
			"role cannot reset it. Every connection to the database starts in that path. "+
			"As the database owner run: %s", old.Database, old.Setting, stmt)
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %w", errLegacySearchPathReset, err)
	}
	logrus.WithFields(logrus.Fields{"database": old.Database, "was": old.Setting}).
		Info("reset the database-level search_path an older migrate wrote")
	return nil
}

// SettleMigrationSearchPath runs before migrating. It first takes back a database-level
// search_path an older migrate wrote, which never stops the migrate, and then refuses a
// connection whose search_path is not the namespace. In that order, a migrate behind a pooler
// that strips the startup parameter still removes the old default before it stops.
func SettleMigrationSearchPath(db *pop.Connection, ns string) error {
	err := ResetLegacyDatabaseSearchPath(db, ns)
	switch {
	case errors.Is(err, errLegacySearchPathLookup):
		logrus.WithError(err).Warn("could not look for a database-level search_path an older migrate wrote; carrying on")
	case err != nil:
		logrus.WithError(err).Warn("found the database-level search_path an older migrate wrote but could not reset it; carrying on")
	}
	return CheckMigrationSearchPath(db, ns)
}
