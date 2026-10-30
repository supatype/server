package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/url"
	"time"

	"github.com/gobuffalo/pop/v6"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// searchPathDriver is the database/sql driver every auth connection opens with: pgx, plus a
// SET search_path on each new connection. The URL still carries search_path as a startup
// parameter, but a session-mode pooler can drop startup parameters (PgBouncer
// ignore_startup_parameters), and then nothing but the database's own default would decide
// where unqualified names go. The SET does not depend on that.
const searchPathDriver = "pgx-auth-search-path"

func init() {
	sql.Register(searchPathDriver, searchPathPgxDriver{})
	// sqlx picks the placeholder style by driver name; this one speaks Postgres.
	sqlx.BindDriver(searchPathDriver, sqlx.DOLLAR)
}

type searchPathPgxDriver struct{}

func (searchPathPgxDriver) Open(dsn string) (driver.Conn, error) {
	connector, err := searchPathConnector(dsn)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

func (searchPathPgxDriver) OpenConnector(dsn string) (driver.Connector, error) {
	return searchPathConnector(dsn)
}

// dropStartupSearchPath stands in for a pooler that drops startup parameters, for tests.
var dropStartupSearchPath = false

// searchPathConnector parses the DSN as pgx does and, when it names a search_path, sets that
// path on every connection once it is open.
func searchPathConnector(dsn string) (driver.Connector, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	path := cfg.RuntimeParams["search_path"]
	if dropStartupSearchPath {
		delete(cfg.RuntimeParams, "search_path")
	}
	if path != "" {
		// set_config stores the value as given, as the startup parameter does, so SHOW reads
		// back exactly what the URL sent either way.
		value := [][]byte{[]byte(path)}
		cfg.AfterConnect = func(ctx context.Context, conn *pgconn.PgConn) error {
			result := conn.ExecParams(ctx, "SELECT pg_catalog.set_config('search_path', $1, false)", value, nil, nil, nil)
			return result.Read().Err
		}
	}
	return searchPathConnectorOf{stdlib.GetConnector(*cfg)}, nil
}

// searchPathConnectorOf is pgx's connector reporting this driver as its own. database/sql hands
// out connector.Driver() as the DB's driver, and OpenTelemetry wraps whatever that is: pgx's
// own would bypass the search_path set on connect.
type searchPathConnectorOf struct {
	driver.Connector
}

func (searchPathConnectorOf) Driver() driver.Driver { return searchPathPgxDriver{} }

// ConnectionSettings is everything one auth connection pool is built from. Namespace is the
// configured one, already trimmed and validated at load.
type ConnectionSettings struct {
	URL             string
	Dialect         string // the configured driver; the URL's scheme when empty
	Namespace       string
	ApplicationName string // left as the URL has it when empty
	Pool            int
	IdlePool        int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// BuildConnectionDetails is the one place an auth connection's details are made, for serving,
// migrating and the index worker alike. The namespace goes into the URL as search_path and is
// set again on every connection by searchPathDriver. A URL that cannot carry it is an error,
// never silently used without it.
func BuildConnectionDetails(s ConnectionSettings) (*pop.ConnectionDetails, error) {
	withPath, err := EnsurePostgresSearchPathInURL(s.URL, s.Namespace)
	if err != nil {
		return nil, fmt.Errorf("parsing the database URL: %w", err)
	}
	u, err := url.Parse(withPath)
	if err != nil {
		return nil, fmt.Errorf("parsing the database URL: %w", err)
	}
	if s.ApplicationName != "" {
		q := u.Query()
		q.Set("application_name", s.ApplicationName)
		u.RawQuery = q.Encode()
	}
	dialect := s.Dialect
	if dialect == "" {
		dialect = u.Scheme
	}
	return &pop.ConnectionDetails{
		Dialect: dialect, Driver: searchPathDriver, URL: u.String(),
		Pool: s.Pool, IdlePool: s.IdlePool, ConnMaxLifetime: s.ConnMaxLifetime, ConnMaxIdleTime: s.ConnMaxIdleTime,
		Options: map[string]string{"migration_table_name": "schema_migrations", "Namespace": s.Namespace},
	}, nil
}
