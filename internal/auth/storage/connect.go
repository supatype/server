package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gobuffalo/pop/v6"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// searchPathDriverPrefix names the database/sql drivers auth connections open with: pgx, plus
// a set of the auth namespace as search_path on each new connection. A Postgres URL still
// carries search_path as a startup parameter too, but a session-mode pooler can drop startup
// parameters (PgBouncer ignore_startup_parameters), and a keyword/value DSN has none; then
// nothing but the database's own default would decide where unqualified names go. The set on
// connect does not depend on either. The namespace is part of the driver, not read back from
// the DSN, so a DSN of any shape gets it.
const searchPathDriverPrefix = "pgx-auth-search-path-"

var (
	searchPathDriversMu sync.Mutex
	searchPathDrivers   = map[string]bool{}
)

// searchPathDriverName is the driver that sets ns on every connection, registered on first use.
func searchPathDriverName(ns string) string {
	name := searchPathDriverPrefix + ns
	searchPathDriversMu.Lock()
	defer searchPathDriversMu.Unlock()
	if !searchPathDrivers[name] {
		sql.Register(name, searchPathPgxDriver{namespace: ns})
		// sqlx picks the placeholder style by driver name; this one speaks Postgres.
		sqlx.BindDriver(name, sqlx.DOLLAR)
		searchPathDrivers[name] = true
	}
	return name
}

type searchPathPgxDriver struct {
	namespace string
}

func (d searchPathPgxDriver) Open(dsn string) (driver.Conn, error) {
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

func (d searchPathPgxDriver) OpenConnector(dsn string) (driver.Connector, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return searchPathConnector{Connector: stdlib.GetConnector(*withSearchPathOnConnect(cfg, d.namespace)), d: d}, nil
}

// withSearchPathOnConnect sets ns as search_path once each connection is open. set_config
// stores the value as given, as the startup parameter does, so SHOW reads back exactly the
// namespace either way.
func withSearchPathOnConnect(cfg *pgx.ConnConfig, ns string) *pgx.ConnConfig {
	value := [][]byte{[]byte(ns)}
	cfg.AfterConnect = func(ctx context.Context, conn *pgconn.PgConn) error {
		result := conn.ExecParams(ctx, "SELECT pg_catalog.set_config('search_path', $1, false)", value, nil, nil, nil)
		return result.Read().Err
	}
	return cfg
}

// searchPathConnector is pgx's connector reporting this driver as its own. database/sql hands
// out connector.Driver() as the DB's driver, and OpenTelemetry wraps whatever that is: pgx's
// own would bypass the search_path set on connect.
type searchPathConnector struct {
	driver.Connector
	d searchPathPgxDriver
}

func (c searchPathConnector) Driver() driver.Driver { return c.d }

// ConnectionSettings is everything one auth connection pool is built from. Namespace is the
// configured one, already trimmed and validated at load.
type ConnectionSettings struct {
	URL             string
	Dialect         string // the configured driver; the URL's scheme when empty
	Namespace       string
	ApplicationName string // set only on a Postgres URL; left as the DSN has it when empty
	Pool            int
	IdlePool        int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// BuildConnectionDetails is the one place an auth connection's details are made, for serving,
// migrating and the index worker alike.
//
// A Postgres URL (postgres:// or postgresql://) gets the namespace as its search_path startup
// parameter, and the application name; one that does not parse is an error. Any other DSN,
// such as keyword/value (`host=db user=auth`) or another scheme, is passed on exactly as
// written. Either way a Postgres connection opens on the auth driver, which sets the namespace
// on every connection, so no DSN shape goes without it.
func BuildConnectionDetails(s ConnectionSettings) (*pop.ConnectionDetails, error) {
	dsn, scheme, err := dsnForNamespace(s)
	if err != nil {
		return nil, err
	}
	dialect := s.Dialect
	if dialect == "" {
		dialect = scheme
	}
	cd := &pop.ConnectionDetails{
		Dialect: dialect, URL: dsn,
		Pool: s.Pool, IdlePool: s.IdlePool, ConnMaxLifetime: s.ConnMaxLifetime, ConnMaxIdleTime: s.ConnMaxIdleTime,
		Options: map[string]string{"migration_table_name": "schema_migrations", "Namespace": s.Namespace},
	}
	if pop.CanonicalDialect(dialect) == "postgres" {
		cd.Driver = searchPathDriverName(s.Namespace)
	}
	return cd, nil
}

// dsnForNamespace adds search_path and application_name to a Postgres URL, returning it with
// its scheme. Anything else is returned as written, with the scheme it names, if any.
func dsnForNamespace(s ConnectionSettings) (dsn, scheme string, err error) {
	if !strings.HasPrefix(s.URL, "postgres://") && !strings.HasPrefix(s.URL, "postgresql://") {
		scheme, _, _ = strings.Cut(s.URL, "://")
		if scheme == s.URL {
			scheme = ""
		}
		return s.URL, scheme, nil
	}
	withPath, err := EnsurePostgresSearchPathInURL(s.URL, s.Namespace)
	if err != nil {
		return "", "", databaseURLError(err)
	}
	u, err := url.Parse(withPath)
	if err != nil {
		return "", "", databaseURLError(err)
	}
	if s.ApplicationName != "" {
		q := u.Query()
		q.Set("application_name", s.ApplicationName)
		u.RawQuery = q.Encode()
	}
	return u.String(), u.Scheme, nil
}

// databaseURLError reports a database URL that does not parse without quoting it. Go's parse
// error carries the whole URL, password included, and this error is logged.
func databaseURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return fmt.Errorf("parsing the database URL: %w", err)
}
