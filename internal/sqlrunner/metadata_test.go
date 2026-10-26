package sqlrunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// A catalogue that does not parse stops the server at start, naming the file,
// rather than serving a Studio whose every Database view is empty.
func TestAnUnparseableCatalogueStopsTheServer(t *testing.T) {
	defer func() {
		got, _ := recover().(string)
		if !strings.Contains(got, "studio_metadata.json") {
			t.Fatalf("panic = %q, want one naming studio_metadata.json", got)
		}
	}()
	mustLoadMetadata([]byte(`{"queries":`))
	t.Fatal("an unparseable catalogue was accepted")
}

func TestMetadataIsPOSTOnly(t *testing.T) {
	noPool := func() (Pool, error) { t.Fatal("must not reach the database"); return nil, nil }
	for name, h := range map[string]http.Handler{
		"metadata":   MetadataHandler(authorised(&config.Config{}), noPool),
		"introspect": IntrospectHandler(authorised(&config.Config{}), noPool),
	} {
		req := httptest.NewRequest(http.MethodGet, "/metadata", nil)
		req.Header.Set("Authorization", "Bearer "+serviceKey)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: GET got %d, want 405", name, rec.Code)
		}
	}
}

func postIntrospect(t *testing.T, pools Pools, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/metadata/introspect", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+serviceKey)
	rec := httptest.NewRecorder()
	IntrospectHandler(authorised(&config.Config{}), pools).ServeHTTP(rec, req)
	return rec
}

// The introspect route runs the introspect query whether or not the caller
// names it, read-only and with the schema bound rather than written in.
func TestTheIntrospectRouteRunsTheIntrospectQuery(t *testing.T) {
	for _, body := range []string{
		`{"params":{"schema":"public"}}`,
		`{"name":"introspect","params":{"schema":"public"}}`,
	} {
		tx := &fakeTx{rows: &fakeRows{columns: []string{"table_name"}, values: [][]any{{"introspected"}}}}
		rec := postIntrospect(t, serving(&fakePool{tx: tx}), body)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"table_name":"introspected"`) {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
		if tx.querySQL != metadataCatalogue.Queries[IntrospectQuery].SQL {
			t.Errorf("%s: ran %q, want the introspect query", body, tx.querySQL)
		}
		if fmt.Sprint(tx.queryArgs) != "[public]" {
			t.Errorf("%s: bound %v, want [public]", body, tx.queryArgs)
		}
		if len(tx.calls) == 0 || tx.calls[0] != "SET TRANSACTION READ ONLY" {
			t.Errorf("%s: the transaction was not made read-only first: %v", body, tx.calls)
		}
		if tx.committed {
			t.Errorf("%s: a read-only query was committed", body)
		}
	}
}

// The narrower route cannot be used to reach the rest of the catalogue: any
// other name is refused before the database is touched.
func TestTheIntrospectRouteRefusesEveryOtherQuery(t *testing.T) {
	noPool := func() (Pool, error) { t.Fatal("must not reach the database"); return nil, nil }
	rec := postIntrospect(t, noPool, `{"name":"roles","params":{"schema":"public"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(errorBody(t, rec), "only the introspect query") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestMetadataFailuresAreReportedWithTheStatusTheyMean(t *testing.T) {
	boom := errors.New("boom")
	for name, tc := range map[string]struct {
		pools Pools
		want  int
		says  string
	}{
		"no database configured": {
			func() (Pool, error) { return nil, boom }, http.StatusServiceUnavailable, "database not available",
		},
		"the pool will not begin a transaction": {
			serving(&fakePool{beginErr: boom}), http.StatusServiceUnavailable, "failed to begin transaction",
		},
		"the transaction cannot be made read-only": {
			serving(&fakePool{tx: &fakeTx{execErr: boom}}), http.StatusInternalServerError, "failed to make the transaction read-only",
		},
		"the query is rejected": {
			serving(&fakePool{tx: &fakeTx{queryErr: boom}}), http.StatusUnprocessableEntity, "boom",
		},
	} {
		rec := postMetadata(t, tc.pools, `{"name":"roles","params":{}}`, serviceKey)
		if rec.Code != tc.want || !strings.Contains(errorBody(t, rec), tc.says) {
			t.Errorf("%s: got %d %s, want %d saying %q", name, rec.Code, rec.Body.String(), tc.want, tc.says)
		}
	}
}

// A read-only query never runs past SET TRANSACTION READ ONLY when that fails.
func TestAMetadataQueryDoesNotRunOnAWritableTransaction(t *testing.T) {
	tx := &fakeTx{execErr: errors.New("boom")}
	postMetadata(t, serving(&fakePool{tx: tx}), `{"name":"roles","params":{}}`, serviceKey)
	if tx.querySQL != "" {
		t.Fatalf("ran %q on a transaction that was not read-only", tx.querySQL)
	}
	if !tx.rolledBack {
		t.Fatal("the transaction was not rolled back")
	}
}
