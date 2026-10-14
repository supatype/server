package sqlrunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/supatype/server/internal/config"
)

// RQ3: Studio's Database views read schema metadata through named, read-only
// queries rather than the SQL runner, so a viewer's views work and nobody with
// schema view can run arbitrary SQL. The control plane carries the same
// catalogue and pins the same hash.
const studioMetadataSHA256 = "bc4b5021f6f1d3ff4424bfb7fbb234467c28bc072c064b8d2d7c40ff73c3bae2"

func TestMetadataCatalogueIsTheSharedDocument(t *testing.T) {
	raw, err := os.ReadFile("studio_metadata.json")
	if err != nil {
		t.Fatalf("the catalogue is missing: %v", err)
	}
	sum := sha256.Sum256(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))
	if got := hex.EncodeToString(sum[:]); got != studioMetadataSHA256 {
		t.Fatalf("studio_metadata.json changed (sha256 %s); change it on both hosts", got)
	}
}

func postMetadata(t *testing.T, pools Pools, body string, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/metadata", strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	MetadataHandler(authorised(&config.Config{}), pools).ServeHTTP(rec, req)
	return rec
}

func TestMetadataRefusesWhatIsNotACatalogueQuery(t *testing.T) {
	noPool := func() (Pool, error) { t.Fatal("must not reach the database"); return nil, nil }
	cases := []struct {
		body string
		want int
	}{
		{`{"name":"pg_shadow","params":{}}`, http.StatusBadRequest},
		{`{"name":"tables","params":{}}`, http.StatusBadRequest},
		{`{"name":"tables","params":{"schema":{"raw":"x"}}}`, http.StatusBadRequest},
		{`not json`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if rec := postMetadata(t, noPool, c.body, serviceKey); rec.Code != c.want {
			t.Fatalf("%s: got %d, want %d", c.body, rec.Code, c.want)
		}
	}
	if rec := postMetadata(t, noPool, `{"name":"roles","params":{}}`, "not-the-key"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a caller without the service role must be refused, got %d", rec.Code)
	}
}

// Every catalogue query runs against a real Postgres, read-only, with its
// parameters bound.
func TestEveryMetadataQueryRuns(t *testing.T) {
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the metadata catalogue against Postgres")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), `CREATE SCHEMA IF NOT EXISTS _supatype;
		CREATE TABLE IF NOT EXISTS _supatype.managed_objects (kind text, schema_name text, parent text DEFAULT '',
		  name text, status text DEFAULT 'managed', migration_id int, updated_at timestamptz DEFAULT now())`); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS _supatype.managed_objects`) })

	sample := map[string]any{"schema": "public", "table": "x", "kind": nil, "status": nil, "table_pattern": "%x%", "tableless": false, "offset": 0}
	pools := func() (Pool, error) { return pool, nil }
	for name, query := range metadataCatalogue.Queries {
		params := map[string]any{}
		for _, p := range query.Params {
			params[p] = sample[p]
		}
		body, _ := json.Marshal(map[string]any{"name": name, "params": params})
		if rec := postMetadata(t, pools, string(body), serviceKey); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}
