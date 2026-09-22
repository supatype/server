package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace/keyspacetest"
	"github.com/supatype/server/internal/restcache"
)

// §12.2 chose one switch for both caches, so enabling a table in cache_tables
// has to register it for the row cache too. These tests are about the seam: the
// allowlist is saved first and the registration follows it, and nothing the row
// cache does may fail a request that already succeeded.
//
// The reconciliation itself is tested in internal/rowcache. What is asserted
// here is which outcomes reach the caller, and in what shape.

// regDB answers by which statement it was sent, matched on substance rather
// than on the exact SQL — the point is that the handler passes the allowlist
// through, not that it spells the query a particular way.
type regDB struct {
	registered map[string]bool
	fail       error
	calls      int
}

func newRegDB(registered ...string) *regDB {
	db := &regDB{registered: map[string]bool{}}
	for _, r := range registered {
		db.registered[r] = true
	}
	return db
}

func (d *regDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.calls++
	if d.fail != nil {
		return nil, d.fail
	}
	// The two function calls first, and matched with their open bracket.
	// "rowcache_reg" is a prefix of "rowcache_register", so a catalogue-first
	// switch swallows every registration and answers it with catalogue rows —
	// which reads back as "no primary key" and passes for a plausible result.
	switch {
	case strings.Contains(sql, "rowcache_register("):
		d.registered[args[0].(string)] = true
		return &regRows{rows: [][]any{{true}}}, nil
	case strings.Contains(sql, "rowcache_unregister("):
		delete(d.registered, args[0].(string))
		return &regRows{rows: [][]any{{true}}}, nil
	case strings.Contains(sql, "unnest"):
		tables, _ := args[1].([]string)
		rows := make([][]any, 0, len(tables))
		for _, t := range tables {
			rows = append(rows, []any{t, "public." + t, true})
		}
		return &regRows{rows: rows}, nil
	case strings.Contains(sql, "supacache.rowcache_reg "):
		rows := [][]any{}
		for q := range d.registered {
			rows = append(rows, []any{q})
		}
		return &regRows{rows: rows}, nil
	}
	return nil, fmt.Errorf("unexpected statement: %s", sql)
}

type regRows struct {
	rows [][]any
	i    int
}

func (r *regRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}

func (r *regRows) Scan(dest ...any) error {
	row := r.rows[r.i-1]
	if len(row) != len(dest) {
		return fmt.Errorf("scan: %d columns into %d destinations", len(row), len(dest))
	}
	// Reported, not asserted: a fake that panics on a mismatch tells you where
	// it happened, and a fake that silently leaves the zero value tells you
	// nothing at all. This one says which column and what it held.
	for i, d := range dest {
		switch t := d.(type) {
		case *string:
			v, ok := row[i].(string)
			if !ok {
				return fmt.Errorf("column %d is %T, not a string", i, row[i])
			}
			*t = v
		case *bool:
			v, ok := row[i].(bool)
			if !ok {
				return fmt.Errorf("column %d is %T, not a bool", i, row[i])
			}
			*t = v
		default:
			return fmt.Errorf("column %d: unsupported destination %T", i, d)
		}
	}
	return nil
}

func (r *regRows) Close()                                       {}
func (r *regRows) Err() error                                   { return nil }
func (r *regRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *regRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *regRows) Values() ([]any, error)                       { return nil, nil }
func (r *regRows) RawValues() [][]byte                          { return nil }
func (r *regRows) Conn() *pgx.Conn                              { return nil }

type restPatchResponse struct {
	Schema      string         `json:"schema"`
	MaxRows     int            `json:"max_rows"`
	CacheMaxTTL int            `json:"cache_max_ttl"`
	CacheTables map[string]any `json:"cache_tables"`
	RowCache    *struct {
		Registered   []string `json:"registered"`
		Unregistered []string `json:"unregistered"`
		Skipped      []struct {
			Table  string `json:"table"`
			Reason string `json:"reason"`
		} `json:"skipped"`
		Unavailable bool   `json:"unavailable"`
		Error       string `json:"error"`
	} `json:"row_cache"`
}

func patchRest(t *testing.T, db StatsQuerier, body string) restPatchResponse {
	t.Helper()
	ks := keyspacetest.New()
	h := Handler(Deps{
		Store:    newMemStore(),
		Config:   devConfig(),
		Cache:    ks,
		Platform: ks,
		Stats:    restcache.NewCounter(),
		Monitor:  db,
	})
	rec := call(t, h, http.MethodPatch, "/config/rest", "", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var got restPatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return got
}

func TestEnablingATableRegistersItForTheRowCache(t *testing.T) {
	db := newRegDB()
	got := patchRest(t, db, `{"cache_tables":{"orders":{"enabled":true}}}`)

	if got.RowCache == nil || len(got.RowCache.Registered) != 1 || got.RowCache.Registered[0] != "orders" {
		t.Fatalf("row_cache = %+v", got.RowCache)
	}
	if !db.registered["public.orders"] {
		t.Fatalf("catalogue = %v", db.registered)
	}
}

func TestDisablingATableUnregistersIt(t *testing.T) {
	// Off in the allowlist, not merely absent from it: the response cache reads
	// Enabled, and so must this.
	db := newRegDB("public.orders")
	got := patchRest(t, db, `{"cache_tables":{"orders":{"enabled":false}}}`)

	if got.RowCache == nil || len(got.RowCache.Unregistered) != 1 {
		t.Fatalf("row_cache = %+v", got.RowCache)
	}
	if db.registered["public.orders"] {
		t.Fatal("still registered")
	}
}

func TestTheResponseStillCarriesTheRestConfigAtTheTopLevel(t *testing.T) {
	// RestCacheBrowser reads cache_max_ttl and cache_tables off the top level.
	// The new field is embedded precisely so that a Studio built against the
	// old shape keeps working, and that is worth asserting rather than assuming.
	got := patchRest(t, newRegDB(),
		`{"schema":"public","max_rows":50,"cache_max_ttl":60,"cache_tables":{"orders":{"enabled":true}}}`)

	if got.Schema != "public" || got.MaxRows != 50 || got.CacheMaxTTL != 60 {
		t.Fatalf("rest config lost: %+v", got)
	}
	if _, ok := got.CacheTables["orders"]; !ok {
		t.Fatalf("cache_tables lost: %+v", got.CacheTables)
	}
}

func TestAPatchThatDoesNotTouchTheAllowlistLeavesTheRowCacheAlone(t *testing.T) {
	// A max_rows change is not a reason for a database round trip, and a
	// reconcile on every PATCH would put one behind edits that have nothing to
	// do with caching.
	db := newRegDB("public.orders")
	got := patchRest(t, db, `{"max_rows":25}`)

	if got.RowCache != nil {
		t.Fatalf("row_cache = %+v, want absent", got.RowCache)
	}
	if db.calls != 0 {
		t.Fatalf("made %d database calls", db.calls)
	}
}

func TestAConvergedSaveReportsNothing(t *testing.T) {
	// Three empty arrays on every save would train a reader to ignore the field.
	db := newRegDB("public.orders")
	got := patchRest(t, db, `{"cache_tables":{"orders":{"enabled":true}}}`)

	if got.RowCache != nil {
		t.Fatalf("row_cache = %+v, want absent", got.RowCache)
	}
}

func TestADatabaseWithNoRowCacheIsReportedAsUnavailableNotAnError(t *testing.T) {
	// The allowlist saved and the response cache is already live. A 502 here
	// would tell someone their change did not take when it did.
	db := newRegDB()
	db.fail = &pgconn.PgError{Code: "42P01", Message: "no such table"}
	got := patchRest(t, db, `{"cache_tables":{"orders":{"enabled":true}}}`)

	if got.RowCache == nil || !got.RowCache.Unavailable {
		t.Fatalf("row_cache = %+v", got.RowCache)
	}
	if got.RowCache.Error != "" {
		t.Fatalf("unavailable is not an error: %q", got.RowCache.Error)
	}
}

func TestNoDatabaseConfiguredIsAlsoUnavailable(t *testing.T) {
	got := patchRest(t, nil, `{"cache_tables":{"orders":{"enabled":true}}}`)
	if got.RowCache == nil || !got.RowCache.Unavailable {
		t.Fatalf("row_cache = %+v", got.RowCache)
	}
}

func TestARealReconcileFailureIsReportedWithoutFailingTheSave(t *testing.T) {
	// Still 200, still saved — and still said out loud, because a registration
	// that did not happen is a cache that will not be faster and a user who
	// should know why.
	db := newRegDB()
	db.fail = &pgconn.PgError{Code: "57014", Message: "canceling statement"}
	got := patchRest(t, db, `{"cache_tables":{"orders":{"enabled":true}}}`)

	if got.RowCache == nil || got.RowCache.Error == "" {
		t.Fatalf("row_cache = %+v", got.RowCache)
	}
	if got.RowCache.Unavailable {
		t.Fatal("a cancelled statement is not a missing extension")
	}
}

func TestTheAllowlistIsSavedEvenWhenTheRowCacheCannotBeReached(t *testing.T) {
	ks := keyspacetest.New()
	store := newMemStore()
	db := newRegDB()
	db.fail = &pgconn.PgError{Code: "57014"}
	h := Handler(Deps{
		Store: store, Config: devConfig(), Cache: ks, Platform: ks,
		Stats: restcache.NewCounter(), Monitor: db,
	})

	if rec := call(t, h, http.MethodPatch, "/config/rest", "",
		`{"cache_tables":{"orders":{"enabled":true}}}`); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	if !store.cfg.Rest.CacheTables["orders"].Enabled {
		t.Fatalf("the allowlist was not saved: %+v", store.cfg.Rest.CacheTables)
	}
}

func TestTheRowCacheIsNotReconciledWhenTheCacheIsNotOffered(t *testing.T) {
	// Refused before anything is read or written, so nothing should reach the
	// database either — a forbidden request that still registered tables would
	// be the paid feature leaking out through its own gate.
	db := newRegDB()
	h := Handler(Deps{
		Store:    newMemStore(),
		Config:   &config.Config{Mode: "managed", ServiceRoleKey: serviceKey},
		Cache:    keyspacetest.New(),
		Platform: keyspacetest.New(),
		Stats:    restcache.NewCounter(),
		Monitor:  db,
	})
	rec := call(t, h, http.MethodPatch, "/config/rest", serviceKey,
		`{"cache_tables":{"orders":{"enabled":true}}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if db.calls != 0 {
		t.Fatalf("made %d database calls on a refused request", db.calls)
	}
}

func TestAConfigWithNoSchemaFallsBackToTheDefault(t *testing.T) {
	// RestConfig.Schema is only empty on a config written before it had the
	// field, or hand-edited. Registering into "" would qualify every table as
	// ".orders", which resolves to nothing — so the tables would all be skipped
	// as missing and the row cache would quietly never come on.
	ks := keyspacetest.New()
	store := newMemStore()
	store.cfg.Rest.Schema = ""
	db := newRegDB()
	h := Handler(Deps{
		Store: store, Config: devConfig(), Cache: ks, Platform: ks,
		Stats: restcache.NewCounter(), Monitor: db,
	})

	rec := call(t, h, http.MethodPatch, "/config/rest", "", `{"cache_tables":{"orders":{"enabled":true}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	if !db.registered["public.orders"] {
		t.Fatalf("catalogue = %v, want the default schema used", db.registered)
	}
}
