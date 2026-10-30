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
	db, err := sql.Open(searchPathDriverName("auth"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, ok := db.Driver().(searchPathPgxDriver); !ok {
		t.Errorf("sql.Open(%q).Driver() is %T, want searchPathPgxDriver", searchPathDriverName("auth"), db.Driver())
	}
	if _, err := otelsql.Register(searchPathDriverName("auth")); err != nil {
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

// DialWithRetry dials again until the database answers. Each dial used to register a new
// OpenTelemetry driver under a new name, so a slow start filled the registry; it is wrapped
// once per driver and the same name handed back.
func TestTheOpenTelemetryDriverIsRegisteredOnce(t *testing.T) {
	first := instrumentForOpenTelemetry(searchPathDriverName("auth"))
	second := instrumentForOpenTelemetry(searchPathDriverName("auth"))
	if first == searchPathDriverName("auth") || first != second {
		t.Errorf("instrumented driver names %q then %q, want one wrapped name reused", first, second)
	}
}

// A keyword/value DSN is not a URL: run through url.Parse and back it came out as a mangled
// path. It is passed on as written, and still gets the namespace set on every connection,
// since its driver is the auth one. A URL of another scheme is passed on untouched too.
func TestADSNThatIsNotAPostgresURLIsPassedOnAsWritten(t *testing.T) {
	keyword := "host=127.0.0.1 port=26543 user=postgres password=pw dbname=auth_db sslmode=disable"
	cd, err := BuildConnectionDetails(ConnectionSettings{URL: keyword, Dialect: "postgres", Namespace: "auth"})
	if err != nil {
		t.Fatal(err)
	}
	if cd.URL != keyword || cd.Driver == "" {
		t.Errorf("keyword DSN became URL %q with driver %q, want it as written on the auth driver", cd.URL, cd.Driver)
	}
	other := "mysql://u:pw@host/db?x=1 2"
	cd, err = BuildConnectionDetails(ConnectionSettings{URL: other, Namespace: "auth"})
	if err != nil {
		t.Fatal(err)
	}
	if cd.URL != other || cd.Dialect != "mysql" {
		t.Errorf("other scheme became %q (dialect %q), want it untouched", cd.URL, cd.Dialect)
	}
}
