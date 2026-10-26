package sqlrunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
const studioMetadataSHA256 = "561408b196b0e13152a43b6a1091f0cf68e137cf96521c9d38d1f126ec05ecec"

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
	pools := ledgerPools(t)
	sample := map[string]any{"schema": "public", "table": "x", "kind": nil, "status": nil, "table_pattern": "%x%", "tableless": false, "offset": 0, "id": "1"}
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

// RQ3b: the Policies lists, Migration History, Seed History and the Database
// settings are catalogue queries too, so a viewer's Studio can read them.
func TestLedgerAndSettingsQueriesAnswer(t *testing.T) {
	pools := ledgerPools(t)
	cases := []struct{ body, want string }{
		{`{"name":"policies","params":{"schema":"public"}}`, `"rows":[`},
		{`{"name":"rls_status","params":{"schema":"public"}}`, `"rows":[`},
		{`{"name":"migrations_audited","params":{}}`, `"audited":true`},
		{`{"name":"migrations","params":{}}`, `"pushed_by":"ada"`},
		{`{"name":"migrations_legacy","params":{}}`, `"pushed_by":null`},
		{`{"name":"migration_sql","params":{"id":"1"}}`, `"sql_up":"CREATE TABLE x ();"`},
		{`{"name":"seeds","params":{}}`, `"file":"seed.ts"`},
		{`{"name":"database_settings","params":{}}`, `"max_connections":`},
	}
	for _, c := range cases {
		rec := postMetadata(t, pools, c.body, serviceKey)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), c.want) {
			t.Fatalf("%s: %d %s, want %s", c.body, rec.Code, rec.Body.String(), c.want)
		}
	}
}

const ledgerSQL = `CREATE SCHEMA IF NOT EXISTS _supatype;
	CREATE TABLE _supatype.managed_objects (kind text, schema_name text, parent text DEFAULT '',
	  name text, status text DEFAULT 'managed', migration_id int, updated_at timestamptz DEFAULT now());
	CREATE TABLE _supatype.migrations (id serial PRIMARY KEY, name text NOT NULL, hash text NOT NULL,
	  applied_at timestamptz NOT NULL DEFAULT now(), rolled_back boolean NOT NULL DEFAULT false, rolled_back_at timestamptz,
	  engine_version text NOT NULL, schema_snapshot jsonb, sql_up text, schema_sources_manifest jsonb, pushed_by text,
	  pushed_via text, pushed_from text, pushed_by_verified boolean, renames jsonb);
	INSERT INTO _supatype.migrations (name, hash, engine_version, sql_up, pushed_by) VALUES ('initial_1', 'h', '0.7.0', 'CREATE TABLE x ();', 'ada');
	CREATE TABLE _supatype.seeds (id bigserial PRIMARY KEY, file text NOT NULL, ir_hash text NOT NULL,
	  schema_fingerprint text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now(), duration_ms bigint NOT NULL DEFAULT 0,
	  status text NOT NULL DEFAULT 'committed', rows_written jsonb NOT NULL DEFAULT '{}'::jsonb, error_message text, engine_version text);
	INSERT INTO _supatype.seeds (file, ir_hash, schema_fingerprint) VALUES ('seed.ts', 'a', 'b');
	CREATE TABLE public.introspected (id int PRIMARY KEY, label text UNIQUE)`

// ledgerPools connects to SUPATYPE_TEST_DSN with the engine's ledgers in
// place, or skips the test when there is no database to run against.
func ledgerPools(t *testing.T) Pools {
	t.Helper()
	dsn := os.Getenv("SUPATYPE_TEST_DSN")
	if dsn == "" {
		t.Skip("set SUPATYPE_TEST_DSN to run the metadata catalogue against Postgres")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	drop := func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS _supatype.managed_objects, _supatype.migrations, _supatype.seeds, public.introspected`)
	}
	drop()
	if _, err := pool.Exec(context.Background(), ledgerSQL); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	t.Cleanup(drop)
	return func() (Pool, error) { return pool, nil }
}

// Data Explorer and the schema screens read a schema's tables and columns
// through the catalogue's introspect query.
func TestIntrospectQueryListsColumns(t *testing.T) {
	rec := postMetadata(t, ledgerPools(t), `{"name":"introspect","params":{"schema":"public"}}`, serviceKey)
	var got struct {
		Rows []struct {
			Table     string `json:"table_name"`
			Column    string `json:"column_name"`
			Type      string `json:"udt_name"`
			IsPrimary bool   `json:"is_primary"`
			IsUnique  bool   `json:"is_unique"`
		} `json:"rows"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var columns []string
	for _, row := range got.Rows {
		if row.Table == "introspected" {
			columns = append(columns, fmt.Sprintf("%s %s pk=%t unique=%t", row.Column, row.Type, row.IsPrimary, row.IsUnique))
		}
	}
	want := "[id int4 pk=true unique=false label text pk=false unique=true]"
	if fmt.Sprint(columns) != want {
		t.Fatalf("columns = %v, want %s", columns, want)
	}
}
