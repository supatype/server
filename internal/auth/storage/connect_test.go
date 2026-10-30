package storage

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/XSAM/otelsql"
)

// With tracing or metrics on, the server wraps its driver for OpenTelemetry by name, and the
// wrapper takes the driver that sql.Open hands back. That has to be this driver: plain pgx
// there would drop the search_path set on connect without a sign.
func TestTheDriverSurvivesWrappingForOpenTelemetry(t *testing.T) {
	db, err := sql.Open(searchPathDriver, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, ok := db.Driver().(searchPathPgxDriver); !ok {
		t.Errorf("sql.Open(%q).Driver() is %T, want searchPathPgxDriver", searchPathDriver, db.Driver())
	}
	if _, err := otelsql.Register(searchPathDriver); err != nil {
		t.Fatal(err)
	}
}

// Go's URL parse error quotes the whole URL, password included, and the error ends up in the
// log. A malformed database URL must be reported without it.
func TestAMalformedDatabaseURLNeverShowsItsPassword(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user:s3cretpw@host:notaport/db",
		"postgres://user:s3cretpw@ho st/db",
		"postgres://user:s3cret\x7fpw@host/db",
		"postgres://user:s3cretpw@[::1/db",
	} {
		_, err := BuildConnectionDetails(ConnectionSettings{URL: dsn, Namespace: "auth"})
		if err == nil {
			t.Errorf("%q: want an error", dsn)
			continue
		}
		if strings.Contains(err.Error(), "s3cret") {
			t.Errorf("the error shows the password: %v", err)
		}
	}
}
