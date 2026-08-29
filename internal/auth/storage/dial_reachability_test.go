package storage

import (
	"strings"
	"testing"

	"github.com/supatype/server/internal/conf"
)

// Dialling has to contact the database.
//
// pop's Open builds the pool and stops there, and lib/pq connects lazily, so a
// dial used to succeed against a database that was not there at all. Everything
// DialWithRetry does — waiting out a database that is merely not up yet, and
// refusing immediately when the credential is wrong — depends on the dial
// producing the failure in the first place, and it never did.
func TestDiallingContactsTheDatabase(t *testing.T) {
	cfg := &conf.GlobalConfiguration{}
	cfg.DB.Driver = "postgres"
	// Nothing listens here, so the dial cannot succeed however lazy the driver
	// would like to be.
	cfg.DB.URL = "postgresql://postgres:postgres@127.0.0.1:1/supatype"

	conn, err := DialContext(t.Context(), cfg)
	if err == nil {
		_ = conn.Close()
		t.Fatal("dialled a database that is not there")
	}
	if !strings.Contains(err.Error(), "checking database connection") {
		t.Errorf("err = %v, want the connection check to be what failed", err)
	}
	// And it is the kind of failure the retry loop waits out rather than the
	// kind it exits on.
	if !isTransientDialError(err) {
		t.Errorf("a database that is not up yet was treated as a misconfiguration: %v", err)
	}
}
