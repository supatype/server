package storage

import (
	"database/sql"
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
