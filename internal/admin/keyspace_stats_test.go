package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace"
	"github.com/supatype/server/internal/data/keyspace/keyspacetest"
	"github.com/supatype/server/internal/restcache"
)

// ─── a StatsQuerier that can produce every state the views can be in ──────────

type fakeRows struct {
	rows   [][]any
	i      int
	closed bool
	err    error
}

func (f *fakeRows) Next() bool {
	if f.i >= len(f.rows) {
		return false
	}
	f.i++
	return true
}

func (f *fakeRows) Scan(dest ...any) error {
	row := f.rows[f.i-1]
	if len(row) != len(dest) {
		return fmt.Errorf("scan: %d columns into %d destinations", len(row), len(dest))
	}
	for i := range dest {
		if err := assign(dest[i], row[i]); err != nil {
			return fmt.Errorf("column %d: %w", i, err)
		}
	}
	return nil
}

func (f *fakeRows) Close()                                       { f.closed = true }
func (f *fakeRows) Err() error                                   { return f.err }
func (f *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (f *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (f *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (f *fakeRows) RawValues() [][]byte                          { return nil }
func (f *fakeRows) Conn() *pgx.Conn                              { return nil }

// assign copies a column into a scan destination, including the two shapes that
// carry the distinction this file exists to defend: a NULL into a pointer, and a
// value into a pointer that was nil.
func assign(dst, src any) error {
	dv := reflect.ValueOf(dst)
	if dv.Kind() != reflect.Ptr || dv.IsNil() {
		return fmt.Errorf("destination is not a non-nil pointer")
	}
	el := dv.Elem()
	if src == nil {
		el.Set(reflect.Zero(el.Type()))
		return nil
	}
	sv := reflect.ValueOf(src)
	switch {
	case sv.Type().AssignableTo(el.Type()):
		el.Set(sv)
	case sv.Type().ConvertibleTo(el.Type()):
		el.Set(sv.Convert(el.Type()))
	case el.Kind() == reflect.Ptr && sv.Type().ConvertibleTo(el.Type().Elem()):
		p := reflect.New(el.Type().Elem())
		p.Elem().Set(sv.Convert(el.Type().Elem()))
		el.Set(p)
	default:
		return fmt.Errorf("cannot put %s into %s", sv.Type(), el.Type())
	}
	return nil
}

type fakeQuerier struct {
	byQuery map[string][][]any
	errs    map[string]error
	// rowErrs is an error raised by rows.Err() rather than by Query: a cursor
	// that dies mid-read, which is a different code path from a query refused.
	rowErrs map[string]error
	queries []string
}

func (f *fakeQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	f.queries = append(f.queries, sql)
	if err, ok := f.errs[key(sql)]; ok {
		return nil, err
	}
	return &fakeRows{rows: f.byQuery[key(sql)], err: f.rowErrs[key(sql)]}, nil
}

// key reduces a query to which view it reads, so the fakes below do not have to
// repeat the SQL.
func key(sql string) string {
	switch sql {
	case keyspaceStatsSQL:
		return "keyspace"
	case rowCacheStatsSQL:
		return "rowcache"
	case rowCacheDatabasesSQL:
		return "databases"
	}
	return "?"
}

func pgErr(code string) error { return &pgconn.PgError{Code: code, Message: code} }

// rowCacheRow builds the 16 columns of pg_stat_keyspace_rowcache in order.
//
// Column 11 is `registrations_loaded`, and it is a **boolean** in the view. This helper passed
// `registrations` again — an int64 — so the fake agreed with a handler that had the same column
// typed as int64, and the whole file passed against a query that answered 502 on every real
// database where the row cache was running. Kept as a named argument rather than derived from
// `registrations`, because "how many are registered" and "have they been read in yet" are
// different questions and conflating them is what hid this.
func rowCacheRow(decode, coherent, slotLost bool, registrations int64, hitPct any) []any {
	return rowCacheRowLoaded(decode, coherent, slotLost, registrations, registrations > 0, hitPct)
}

func rowCacheRowLoaded(
	decode, coherent, slotLost bool,
	registrations int64,
	registrationsLoaded bool,
	hitPct any,
) []any {
	return []any{
		int64(12), int64(90), int64(10), int64(4096), int64(1 << 20), hitPct,
		coherent, decode, any(int64(40)), int64(200),
		registrations, registrationsLoaded, "proj_db", slotLost,
		int64(1), int64(0),
	}
}

func statsHandler(t *testing.T, q StatsQuerier) http.Handler {
	t.Helper()
	ks := keyspacetest.New()
	return Handler(Deps{
		Store:    newMemStore(),
		Config:   devConfig(),
		Cache:    keyspace.Client(ks),
		Platform: keyspace.Client(ks),
		Stats:    restcache.NewCounter(),
		Monitor:  q,
	})
}

var _ apiconfig.Store = (*memStore)(nil)

func rowCacheBody(t *testing.T, q StatsQuerier) rowCacheStatus {
	t.Helper()
	rec := call(t, statsHandler(t, q), http.MethodGet, "/cache/rowcache", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got rowCacheStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

// ─── rule 2: a switched-off feature returns NO ROWS, not zeroes ───────────────

func TestRowCacheOffIsNotAHealthyCache(t *testing.T) {
	// pg_stat_keyspace_invalidation is empty with rowcache_decode off. A handler
	// that unpacked rows into a struct and returned it would report a disabled
	// feature as a perfectly healthy one with no traffic — which is worse than
	// reporting nothing, because it answers the question wrongly rather than not
	// answering it.
	got := rowCacheBody(t, &fakeQuerier{byQuery: map[string][][]any{"rowcache": nil}})
	if got.State != "off" {
		t.Fatalf("state = %q, want off", got.State)
	}
	if got.Databases != nil {
		t.Fatalf("a cache that is off has no per-database detail, got %+v", got.Databases)
	}
}

func TestRowCacheDecodeOffWinsOverTheCoherentColumn(t *testing.T) {
	// The view answers even with decoding off, and reports coherent = true —
	// correctly, because with no decoder the operator owns coherence and nothing
	// fails closed. Reported as-is that reads "healthy", so the state has to be
	// decided by decode_enabled rather than by the column.
	got := rowCacheBody(t, &fakeQuerier{byQuery: map[string][][]any{
		"rowcache": {rowCacheRow(false, true, false, 4, 99.0)},
	}})
	if got.State != "off" {
		t.Fatalf("state = %q, want off even though coherent is true", got.State)
	}
	if got.Coherent != true {
		t.Fatalf("the column itself should still be reported, got %v", got.Coherent)
	}
}

func TestRowCacheRegisteredAndCurrentIsParticipating(t *testing.T) {
	got := rowCacheBody(t, &fakeQuerier{byQuery: map[string][][]any{
		"rowcache":  {rowCacheRow(true, true, false, 3, 87.5)},
		"databases": {{"proj_db", "participating", int64(3), true, false, any(int64(40)), int64(200), any(int32(909))}},
	}})
	if got.State != "participating" {
		t.Fatalf("state = %q, want participating", got.State)
	}
	if len(got.Databases) != 1 || got.Databases[0].Database != "proj_db" {
		t.Fatalf("per-database detail missing: %+v", got.Databases)
	}
	if got.Databases[0].WorkerPID == nil || *got.Databases[0].WorkerPID != 909 {
		t.Fatalf("worker_pid not carried: %+v", got.Databases[0])
	}
}

func TestRowCacheOnButNothingRegisteredIsIdleNotBroken(t *testing.T) {
	// A database with no registrations deliberately gets no slot and no turn.
	// That is the state every paid project is in until P8 supplies cache_tables,
	// and it must not read as a fault.
	got := rowCacheBody(t, &fakeQuerier{byQuery: map[string][][]any{
		"rowcache":  {rowCacheRow(true, true, false, 0, nil)},
		"databases": {},
	}})
	if got.State != "idle" {
		t.Fatalf("state = %q, want idle", got.State)
	}
}

func TestRowCacheBehindOrSlotLostIsIncoherent(t *testing.T) {
	for _, tc := range []struct {
		name               string
		coherent, slotLost bool
	}{
		{"behind its staleness window", false, false},
		{"slot cut loose for retaining WAL", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rowCacheBody(t, &fakeQuerier{byQuery: map[string][][]any{
				"rowcache":  {rowCacheRow(true, tc.coherent, tc.slotLost, 3, 87.5)},
				"databases": {},
			}})
			if got.State != "incoherent" {
				t.Fatalf("state = %q, want incoherent", got.State)
			}
		})
	}
}

// ─── rule 4: hit_pct is NULL, not 0, before the first lookup ──────────────────

func TestHitPctSurvivesAsNullRatherThanZero(t *testing.T) {
	// "No traffic yet" and "0% hit rate" are different states. A fresh cache
	// reading 0% looks broken and is not, and the extension goes out of its way
	// to return NULL here — collapsing it in the JSON would throw that away.
	got := rowCacheBody(t, &fakeQuerier{byQuery: map[string][][]any{
		"rowcache":  {rowCacheRow(true, true, false, 2, nil)},
		"databases": {},
	}})
	if got.HitPct != nil {
		t.Fatalf("hit_pct = %v, want null", *got.HitPct)
	}
	raw := call(t, statsHandler(t, &fakeQuerier{byQuery: map[string][][]any{
		"rowcache":  {rowCacheRow(true, true, false, 2, nil)},
		"databases": {},
	}}), http.MethodGet, "/cache/rowcache", "", "")
	// Asserted on the wire, not just the struct: `omitempty` on a *float64 would
	// drop the key entirely, and an absent key is a third state a UI has to guess at.
	if !jsonHasNullKey(t, raw.Body.Bytes(), "hit_pct") {
		t.Fatalf("hit_pct should be present and null, got %s", raw.Body.String())
	}
}

func jsonHasNullKey(t *testing.T, body []byte, key string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	v, ok := m[key]
	return ok && string(v) == "null"
}

// ─── the extension not being there is a supported deployment ──────────────────

func TestRowCacheUnavailableWhenTheExtensionIsAbsentOrUnreadable(t *testing.T) {
	for _, code := range []string{"42P01", "42501", "3F000"} {
		t.Run(code, func(t *testing.T) {
			// 42501 is the one every fresh provision hits: the views exist and the
			// role is not a pg_monitor member. It is a grant someone has to add,
			// not an outage, and a 502 would send whoever reads it looking for one.
			got := rowCacheBody(t, &fakeQuerier{errs: map[string]error{"rowcache": pgErr(code)}})
			if got.State != "unavailable" {
				t.Fatalf("state = %q, want unavailable", got.State)
			}
		})
	}
}

func TestRowCacheWithNoDatabaseConfiguredIsUnavailableNotAnError(t *testing.T) {
	got := rowCacheBody(t, nil)
	if got.State != "unavailable" {
		t.Fatalf("state = %q, want unavailable", got.State)
	}
}

func TestRowCacheReportsARealFailureAsOne(t *testing.T) {
	// Everything that is not "the extension is not there" still has to fail. The
	// unavailable state is for a known shape of absence, not a catch-all.
	rec := call(t, statsHandler(t, &fakeQuerier{errs: map[string]error{"rowcache": pgErr("57014")}}),
		http.MethodGet, "/cache/rowcache", "", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

// ─── rule 3: stale_after_ms is a promise, read live ───────────────────────────

func TestStaleAfterIsReportedFromTheViewRatherThanAssumed(t *testing.T) {
	// It grows with the number of participating databases once they outnumber
	// the invalidation pool, so a UI that prints the 200ms default understates
	// the window it is promising a user.
	q := &fakeQuerier{byQuery: map[string][][]any{
		"rowcache":  {rowCacheRow(true, true, false, 3, 87.5)},
		"databases": {},
	}}
	q.byQuery["rowcache"][0][9] = int64(1400)
	got := rowCacheBody(t, q)
	if got.StaleAfterMS != 1400 {
		t.Fatalf("stale_after_ms = %d, want 1400", got.StaleAfterMS)
	}
}

// ─── rule 1: counters are cumulative ──────────────────────────────────────────

func TestKeyspaceCountersAreNamedAsTotals(t *testing.T) {
	// hits/misses/evictions count since the segment started and are not reset by
	// reading. The _total suffix is the whole defence: a caller that computes a
	// rate from these without a delta gets 94% on a cache broken for an hour.
	rec := call(t, statsHandler(t, &fakeQuerier{byQuery: map[string][][]any{
		"keyspace": {{4, 8, int64(120), int64(900), int64(100), int64(3), int64(1000), int64(0), int64(2), int64(4096), int64(1 << 20), 90.0}},
	}}), http.MethodGet, "/cache/keyspace", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"hits_total", "misses_total", "evictions_total", "sets_total"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("%q missing from %s", k, rec.Body.String())
		}
	}
	for _, k := range []string{"hits", "misses", "evictions"} {
		if _, ok := m[k]; ok {
			t.Fatalf("%q is cumulative and must not be served under a bare name", k)
		}
	}
}

func TestKeyspaceStatsWithoutTheExtensionSaysSo(t *testing.T) {
	rec := call(t, statsHandler(t, &fakeQuerier{byQuery: map[string][][]any{"keyspace": nil}}),
		http.MethodGet, "/cache/keyspace", "", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// ─── the routes are service-role gated like the rest of admin.Handler ─────────

func TestStatsRoutesRequireTheServiceRole(t *testing.T) {
	cfg := &config.Config{Mode: "managed", ServiceRoleKey: serviceKey}
	ks := keyspacetest.New()
	h := Handler(Deps{
		Store: newMemStore(), Config: cfg, Cache: ks, Platform: ks,
		Stats: restcache.NewCounter(), Monitor: &fakeQuerier{},
	})
	for _, path := range []string{"/cache/keyspace", "/cache/rowcache"} {
		if rec := call(t, h, http.MethodGet, path, "", ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s without a token = %d, want 403", path, rec.Code)
		}
	}
}

func TestStatsRoutesAreReadOnly(t *testing.T) {
	h := statsHandler(t, &fakeQuerier{})
	for _, path := range []string{"/cache/keyspace", "/cache/rowcache"} {
		if rec := call(t, h, http.MethodDelete, path, "", ""); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("DELETE %s = %d, want 405", path, rec.Code)
		}
	}
}

// ─── every way the read can fail, and which of them are states ────────────────

func TestKeyspaceStatsWithNoDatabaseConfigured(t *testing.T) {
	// Unlike the row cache, this one has no "unavailable" state to report: it is
	// a single aggregate row or nothing, so there is nothing to render.
	rec := call(t, statsHandler(t, nil), http.MethodGet, "/cache/keyspace", "", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestKeyspaceStatsQueryFailureIsABadGateway(t *testing.T) {
	rec := call(t, statsHandler(t, &fakeQuerier{errs: map[string]error{"keyspace": pgErr("57014")}}),
		http.MethodGet, "/cache/keyspace", "", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestKeyspaceStatsScanMismatchIsReported(t *testing.T) {
	// A view that grew or lost a column between the extension and this binary.
	// It has to fail rather than serve a struct half-filled with zeroes, because
	// zeroes here are indistinguishable from an idle cache.
	rec := call(t, statsHandler(t, &fakeQuerier{byQuery: map[string][][]any{
		"keyspace": {{1, 2, int64(3)}},
	}}), http.MethodGet, "/cache/keyspace", "", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestKeyspaceStatsCursorFailureIsReported(t *testing.T) {
	rec := call(t, statsHandler(t, &fakeQuerier{
		byQuery: map[string][][]any{"keyspace": {{4, 8, int64(1), int64(1), int64(1), int64(1), int64(1), int64(1), int64(1), int64(1), int64(1), 50.0}}},
		rowErrs: map[string]error{"keyspace": pgErr("08006")},
	}), http.MethodGet, "/cache/keyspace", "", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestRowCacheEmptyResultWithACursorErrorIsNotReadAsOff(t *testing.T) {
	// No rows AND an error is a read that failed, not a feature that is off.
	// Collapsing the two would report a broken connection as a disabled cache.
	rec := call(t, statsHandler(t, &fakeQuerier{
		byQuery: map[string][][]any{"rowcache": nil},
		rowErrs: map[string]error{"rowcache": pgErr("08006")},
	}), http.MethodGet, "/cache/rowcache", "", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestRowCacheScanMismatchIsReported(t *testing.T) {
	rec := call(t, statsHandler(t, &fakeQuerier{byQuery: map[string][][]any{
		"rowcache": {{int64(1), int64(2)}},
	}}), http.MethodGet, "/cache/rowcache", "", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestRowCacheCursorFailureAfterARowIsReported(t *testing.T) {
	rec := call(t, statsHandler(t, &fakeQuerier{
		byQuery: map[string][][]any{"rowcache": {rowCacheRow(true, true, false, 3, 87.5)}},
		rowErrs: map[string]error{"rowcache": pgErr("08006")},
	}), http.MethodGet, "/cache/rowcache", "", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestPerDatabaseDetailIsOptionalButItsFailuresAreNot(t *testing.T) {
	base := map[string][][]any{"rowcache": {rowCacheRow(true, true, false, 3, 87.5)}}

	t.Run("absent view leaves the summary standing", func(t *testing.T) {
		// The per-database view arrived in a later extension version than the
		// summary. An older instance still has a useful answer to give.
		got := rowCacheBody(t, &fakeQuerier{
			byQuery: base,
			errs:    map[string]error{"databases": pgErr("42P01")},
		})
		if got.State != "participating" {
			t.Fatalf("state = %q, want participating", got.State)
		}
		if got.Databases != nil {
			t.Fatalf("want no per-database detail, got %+v", got.Databases)
		}
	})

	t.Run("a real failure is not swallowed", func(t *testing.T) {
		rec := call(t, statsHandler(t, &fakeQuerier{
			byQuery: base,
			errs:    map[string]error{"databases": pgErr("57014")},
		}), http.MethodGet, "/cache/rowcache", "", "")
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
	})

	t.Run("a scan mismatch is not swallowed", func(t *testing.T) {
		rec := call(t, statsHandler(t, &fakeQuerier{
			byQuery: map[string][][]any{
				"rowcache":  {rowCacheRow(true, true, false, 3, 87.5)},
				"databases": {{"proj_db"}},
			},
		}), http.MethodGet, "/cache/rowcache", "", "")
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
	})

	t.Run("a cursor failure is not swallowed", func(t *testing.T) {
		rec := call(t, statsHandler(t, &fakeQuerier{
			byQuery: map[string][][]any{
				"rowcache":  {rowCacheRow(true, true, false, 3, 87.5)},
				"databases": {},
			},
			rowErrs: map[string]error{"databases": pgErr("08006")},
		}), http.MethodGet, "/cache/rowcache", "", "")
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
	})
}

func TestAnErrorThatIsNotPostgresIsNotMistakenForAMissingExtension(t *testing.T) {
	// errors.As finds nothing, so the absence test has to answer false rather
	// than fall through to a nil-pointer read of a code that is not there.
	if isMissingRelation(context.Canceled) {
		t.Fatal("a context cancellation is not a missing relation")
	}
}

// TestRegistrationsLoadedIsABooleanBecauseTheViewSaysSo pins the one column that cost a diagnosis.
//
// `supacache.pg_stat_keyspace_rowcache.registrations_loaded` is `boolean`. This handler scanned it
// into an `int64`, so every read of the view failed with
//
//	cannot scan bool (OID 16) in binary format into *int64
//
// and `/admin/v1/cache/rowcache` answered 502.
//
// It shipped because the failure is invisible until the feature works. The view is EMPTY while
// `rowcache_decode` is off: no row, no scan, no error, and the handler correctly reports `off`.
// Turning the row cache on is what produces a row to scan, so the panel reported the one database
// where the cache was genuinely running as one where it was not — and Studio renders any
// non-404/503 failure with the same words it uses for "off", which points the reader at their
// schema rather than at the endpoint.
//
// Asserted with a real bool rather than through the shared helper, so a future edit to that helper
// cannot quietly restore the old assumption.
func TestRegistrationsLoadedIsABooleanBecauseTheViewSaysSo(t *testing.T) {
	for _, loaded := range []bool{true, false} {
		got := rowCacheBody(t, &fakeQuerier{byQuery: map[string][][]any{
			"rowcache": {rowCacheRowLoaded(true, true, false, 4, loaded, 87.5)},
		}})
		if got.RegistrationsLoaded != loaded {
			t.Errorf("registrations_loaded = %v, want %v", got.RegistrationsLoaded, loaded)
		}
	}
}
