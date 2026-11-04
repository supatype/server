package changelog

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// The SQLSTATEs and the allowlist message were measured on supatype/postgres:latest (2026-11-03).
func TestUnsupportedReasons(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&pgconn.PgError{Code: "55000", Message: `logical decoding requires "wal_level" >= "logical"`}, "wal_level is not logical"},
		{&pgconn.PgError{Code: "58P01", Message: `could not access file "wal2json"`}, "the wal2json output plugin is not installed"},
		{&pgconn.PgError{Code: "42501", Message: "permission denied to use replication slots"}, "the database role may not use replication slots"},
		{&pgconn.PgError{Code: "42501", Message: `library "wal2json" may not be used as an output plugin`}, "wal2json is not in output_plugin_libraries"},
		{fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "55000"}), "wal_level is not logical"},
		{&pgconn.PgError{Code: "08006", Message: "connection failure"}, ""},
		{errors.New("dial tcp: refused"), ""},
	}
	for _, tc := range cases {
		if got := unsupportedReason(tc.err); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.err, got, tc.want)
		}
	}
	var unsupported *UnsupportedError
	if !errors.As(classify(cases[0].err), &unsupported) {
		t.Error("an unsupported database must classify as UnsupportedError")
	}
	if errors.As(classify(cases[5].err), &unsupported) {
		t.Error("a lost connection is transient, not unsupported")
	}
}
