package rowcache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ─── a DB that can be every shape the real one takes ─────────────────────────

type fakeRows struct {
	rows [][]any
	i    int
	err  error
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
		switch d := dest[i].(type) {
		case *string:
			v, ok := row[i].(string)
			if !ok {
				return fmt.Errorf("column %d is not a string", i)
			}
			*d = v
		case *bool:
			v, ok := row[i].(bool)
			if !ok {
				return fmt.Errorf("column %d is not a bool", i)
			}
			*d = v
		default:
			return fmt.Errorf("column %d: unsupported destination %T", i, dest[i])
		}
	}
	return nil
}

func (f *fakeRows) Close()                                       {}
func (f *fakeRows) Err() error                                   { return f.err }
func (f *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (f *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (f *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (f *fakeRows) RawValues() [][]byte                          { return nil }
func (f *fakeRows) Conn() *pgx.Conn                              { return nil }

type call struct {
	sql string
	arg string
}

// fakeDB answers by which statement was sent. `registered` is the mutable
// catalogue, so a reconcile's writes are visible to its own later reads —
// without that a test proves the calls were made but not that they converge.
type fakeDB struct {
	registered map[string]bool // qualified name -> present
	missing    map[string]bool // tables to_regclass cannot resolve
	noPK       map[string]bool // tables rowcache_register refuses
	errs       map[string]error
	rowErrs    map[string]error
	calls      []call
	schema     string
}

func newDB(schema string, registered ...string) *fakeDB {
	db := &fakeDB{
		registered: map[string]bool{},
		missing:    map[string]bool{},
		noPK:       map[string]bool{},
		errs:       map[string]error{},
		rowErrs:    map[string]error{},
		schema:     schema,
	}
	for _, r := range registered {
		db.registered[schema+"."+r] = true
	}
	return db
}

func which(sql string) string {
	switch sql {
	case wantedSQL:
		return "wanted"
	case currentSQL:
		return "current"
	case registerSQL:
		return "register"
	case unregisterSQL:
		return "unregister"
	}
	return "?"
}

func (f *fakeDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	kind := which(sql)
	arg := ""
	if kind == "register" || kind == "unregister" {
		arg, _ = args[0].(string)
	}
	f.calls = append(f.calls, call{sql: kind, arg: arg})

	if err, ok := f.errs[kind]; ok {
		return nil, err
	}
	rowErr := f.rowErrs[kind]

	switch kind {
	case "wanted":
		tables, _ := args[1].([]string)
		var rows [][]any
		for _, t := range tables {
			rows = append(rows, []any{t, f.schema + "." + t, !f.missing[t]})
		}
		return &fakeRows{rows: rows, err: rowErr}, nil
	case "current":
		var names []string
		for q := range f.registered {
			names = append(names, q)
		}
		sort.Strings(names)
		var rows [][]any
		for _, q := range names {
			rows = append(rows, []any{q})
		}
		return &fakeRows{rows: rows, err: rowErr}, nil
	case "register":
		bare := arg[len(f.schema)+1:]
		if f.noPK[bare] {
			return &fakeRows{rows: [][]any{{false}}, err: rowErr}, nil
		}
		f.registered[arg] = true
		return &fakeRows{rows: [][]any{{true}}, err: rowErr}, nil
	case "unregister":
		had := f.registered[arg]
		delete(f.registered, arg)
		return &fakeRows{rows: [][]any{{had}}, err: rowErr}, nil
	}
	return &fakeRows{}, nil
}

func (f *fakeDB) did(kind string) []string {
	var out []string
	for _, c := range f.calls {
		if c.sql == kind {
			out = append(out, c.arg)
		}
	}
	return out
}

func pgErr(code string) error { return &pgconn.PgError{Code: code, Message: code} }

func reconcile(t *testing.T, db DB, enabled ...string) Outcome {
	t.Helper()
	out, err := Reconcile(context.Background(), db, "public", enabled)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return out
}

// ─── the allowlist is the switch ─────────────────────────────────────────────

func TestATableTurnedOnIsRegistered(t *testing.T) {
	db := newDB("public")
	out := reconcile(t, db, "orders")

	if !reflect.DeepEqual(out.Registered, []string{"orders"}) {
		t.Fatalf("registered = %v, want [orders]", out.Registered)
	}
	if got := db.did("register"); !reflect.DeepEqual(got, []string{"public.orders"}) {
		t.Fatalf("registered %v, want [public.orders]", got)
	}
}

func TestATableTurnedOffIsUnregistered(t *testing.T) {
	db := newDB("public", "orders", "invoices")
	out := reconcile(t, db, "orders")

	if !reflect.DeepEqual(out.Unregistered, []string{"invoices"}) {
		t.Fatalf("unregistered = %v, want [invoices]", out.Unregistered)
	}
	if db.registered["public.orders"] != true {
		t.Fatal("orders should still be registered")
	}
}

func TestAnEmptyAllowlistClearsEverything(t *testing.T) {
	// Turning the feature off has to actually turn it off. This is also the case
	// the package doc refuses to run at startup: correct when somebody asked for
	// it, destructive when a lost config file asked for it by accident.
	db := newDB("public", "orders", "invoices")
	out := reconcile(t, db)

	sort.Strings(out.Unregistered)
	if !reflect.DeepEqual(out.Unregistered, []string{"invoices", "orders"}) {
		t.Fatalf("unregistered = %v, want both", out.Unregistered)
	}
	if len(db.registered) != 0 {
		t.Fatalf("catalogue not empty: %v", db.registered)
	}
}

func TestReconcilingTwiceChangesNothingTheSecondTime(t *testing.T) {
	// Declarative, not a delta. A second call with the same intent must be a
	// no-op, or every config save would churn registrations the planner depends
	// on and evict the pinned catalogue entries for no reason.
	db := newDB("public", "invoices")
	first := reconcile(t, db, "orders")
	if !first.Changed() {
		t.Fatal("the first call should have done something")
	}

	db.calls = nil
	second := reconcile(t, db, "orders")
	if second.Changed() {
		t.Fatalf("second call did work: %+v", second)
	}
	if got := db.did("register"); got != nil {
		t.Fatalf("re-registered %v", got)
	}
	if got := db.did("unregister"); got != nil {
		t.Fatalf("re-unregistered %v", got)
	}
}

func TestOnlyEnabledTablesAreRegistered(t *testing.T) {
	// The caller filters on Enabled; this asserts the contract from the other
	// side — an allowlist entry that is present but off is simply not in the set.
	db := newDB("public", "invoices")
	out := reconcile(t, db, "orders")
	if !reflect.DeepEqual(out.Registered, []string{"orders"}) ||
		!reflect.DeepEqual(out.Unregistered, []string{"invoices"}) {
		t.Fatalf("outcome = %+v", out)
	}
}

// ─── the two caches ride one switch, and are not one cache ───────────────────

func TestATableWithNoPrimaryKeyIsSkippedRatherThanFailing(t *testing.T) {
	// rowcache_register returns false, not an error: the row cache keys on the
	// primary key and there is nothing to key on. The response cache still
	// applies to that table, so refusing the whole allowlist over it would turn
	// a partial win into an error message.
	db := newDB("public")
	db.noPK["events"] = true
	out := reconcile(t, db, "events", "orders")

	if !reflect.DeepEqual(out.Registered, []string{"orders"}) {
		t.Fatalf("registered = %v, want [orders]", out.Registered)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Table != "events" {
		t.Fatalf("skipped = %+v", out.Skipped)
	}
	if out.Skipped[0].Reason == "" {
		t.Fatal("a skip with no reason is not worth reporting")
	}
}

func TestATableThatDoesNotExistYetIsSkipped(t *testing.T) {
	// A config pushed before the migration that creates the table. to_regclass
	// returns NULL rather than raising, which is why the query uses it — the
	// next reconcile picks the table up.
	db := newDB("public")
	db.missing["not_yet"] = true
	out := reconcile(t, db, "not_yet", "orders")

	if !reflect.DeepEqual(out.Registered, []string{"orders"}) {
		t.Fatalf("registered = %v", out.Registered)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Table != "not_yet" {
		t.Fatalf("skipped = %+v", out.Skipped)
	}
	if got := db.did("register"); !reflect.DeepEqual(got, []string{"public.orders"}) {
		t.Fatalf("tried to register a table that is not there: %v", got)
	}
}

// ─── what it will not touch ──────────────────────────────────────────────────

func TestItLeavesRegistrationsInOtherSchemasAlone(t *testing.T) {
	// The catalogue query is scoped to the configured schema, so a registration
	// someone made by hand elsewhere is not this allowlist's to remove. Modelled
	// here by a catalogue whose rows the schema-scoped query never returns.
	db := newDB("public", "orders")
	db.registered["reporting.rollups"] = true
	// currentSQL filters by schema; the fake returns everything, so filter the
	// way the query does before comparing.
	out, err := Reconcile(context.Background(), &schemaScoped{fakeDB: db, schema: "public"}, "public", []string{"orders"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Changed() {
		t.Fatalf("touched something: %+v", out)
	}
	if !db.registered["reporting.rollups"] {
		t.Fatal("unregistered a table in another schema")
	}
}

// schemaScoped makes the fake catalogue behave like currentSQL's WHERE clause.
type schemaScoped struct {
	*fakeDB
	schema string
}

func (s *schemaScoped) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if which(sql) != "current" {
		return s.fakeDB.Query(ctx, sql, args...)
	}
	s.fakeDB.calls = append(s.fakeDB.calls, call{sql: "current"})
	var rows [][]any
	var names []string
	for q := range s.fakeDB.registered {
		names = append(names, q)
	}
	sort.Strings(names)
	for _, q := range names {
		if len(q) > len(s.schema) && q[:len(s.schema)+1] == s.schema+"." {
			rows = append(rows, []any{q})
		}
	}
	return &fakeRows{rows: rows}, nil
}

// ─── a database with no row cache is a supported database ────────────────────

func TestNoExtensionIsUnavailableRatherThanAFailure(t *testing.T) {
	for _, code := range []string{"42P01", "42501", "3F000", "42883"} {
		t.Run(code, func(t *testing.T) {
			db := newDB("public")
			db.errs["wanted"] = pgErr(code)
			_, err := Reconcile(context.Background(), db, "public", []string{"orders"})
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
		})
	}
}

func TestTheCatalogueBeingAbsentIsAlsoUnavailable(t *testing.T) {
	db := newDB("public")
	db.errs["current"] = pgErr("42P01")
	_, err := Reconcile(context.Background(), db, "public", []string{"orders"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestNoDatabaseAtAllIsUnavailable(t *testing.T) {
	_, err := Reconcile(context.Background(), nil, "public", []string{"orders"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestAnEmptyAllowlistOnADatabaseWithNoRowCacheStillReportsUnavailable(t *testing.T) {
	// The wanted query is skipped when nothing is enabled, so the catalogue read
	// is the only thing that can report it. Without this the "turn it all off"
	// path would claim success on a database that has no row cache to turn off.
	db := newDB("public")
	db.errs["current"] = pgErr("3F000")
	_, err := Reconcile(context.Background(), db, "public", nil)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// ─── real failures are still failures ────────────────────────────────────────

func TestARealErrorIsNotMistakenForAMissingExtension(t *testing.T) {
	for _, kind := range []string{"wanted", "current", "register"} {
		t.Run(kind, func(t *testing.T) {
			db := newDB("public")
			db.errs[kind] = pgErr("57014") // query_canceled
			_, err := Reconcile(context.Background(), db, "public", []string{"orders"})
			if err == nil || errors.Is(err, ErrUnavailable) {
				t.Fatalf("err = %v, want a real failure", err)
			}
		})
	}
}

func TestAnUnregisterFailureIsReported(t *testing.T) {
	db := newDB("public", "invoices")
	db.errs["unregister"] = pgErr("57014")
	_, err := Reconcile(context.Background(), db, "public", nil)
	if err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want a real failure", err)
	}
}

func TestACursorFailureIsReported(t *testing.T) {
	for _, kind := range []string{"wanted", "current"} {
		t.Run(kind, func(t *testing.T) {
			db := newDB("public", "invoices")
			db.rowErrs[kind] = pgErr("08006")
			_, err := Reconcile(context.Background(), db, "public", []string{"orders"})
			if err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestAScanMismatchIsReported(t *testing.T) {
	db := &shortRows{newDB("public")}
	_, err := Reconcile(context.Background(), db, "public", []string{"orders"})
	if err == nil {
		t.Fatal("want an error")
	}
}

type shortRows struct{ *fakeDB }

func (s *shortRows) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if which(sql) == "wanted" {
		return &fakeRows{rows: [][]any{{"orders"}}}, nil
	}
	return s.fakeDB.Query(ctx, sql, args...)
}

// ─── the reporting contract ──────────────────────────────────────────────────

func TestChangedIsFalseOnlyWhenNothingHappened(t *testing.T) {
	// The handler leaves row_cache out of its response when this is false, so a
	// converged save does not carry three empty arrays.
	if (Outcome{}).Changed() {
		t.Fatal("an empty outcome changed nothing")
	}
	for _, o := range []Outcome{
		{Registered: []string{"a"}},
		{Unregistered: []string{"a"}},
		{Skipped: []Skip{{Table: "a"}}},
	} {
		if !o.Changed() {
			t.Fatalf("%+v should count as changed", o)
		}
	}
}

func TestBareNameUndoesTheQuotingTheCatalogueAdds(t *testing.T) {
	// rowcache_reg stores format('%I.%I', ...), which quotes anything that needs
	// it. The allowlist uses plain names, so a table needing quotes would look
	// like a different table on every reconcile and be unregistered and
	// re-registered forever.
	for _, tc := range []struct{ qualified, schema, want string }{
		{"public.orders", "public", "orders"},
		{`public."My Table"`, "public", "My Table"},
		{`"My Schema"."My Table"`, "My Schema", "My Table"},
		{"otherthing", "public", "otherthing"},
	} {
		if got := bareName(tc.qualified, tc.schema); got != tc.want {
			t.Errorf("bareName(%q, %q) = %q, want %q", tc.qualified, tc.schema, got, tc.want)
		}
	}
}

// A catalogue row that will not scan, and a register/unregister that answers
// nothing. Neither should happen against a real pg_keyspace — the catalogue's
// tbl is text and both functions return boolean — but "cannot happen" is the
// reason the branches were there to write, and an unchecked one is how a
// reconcile reports success having done nothing.

type badCatalogue struct{ *fakeDB }

func (b *badCatalogue) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if which(sql) == "current" {
		return &fakeRows{rows: [][]any{{true}}}, nil // a bool where tbl should be
	}
	return b.fakeDB.Query(ctx, sql, args...)
}

func TestACatalogueRowThatWillNotScanIsReported(t *testing.T) {
	_, err := Reconcile(context.Background(), &badCatalogue{newDB("public")}, "public", []string{"orders"})
	if err == nil {
		t.Fatal("want an error")
	}
}

type emptyAnswer struct {
	*fakeDB
	to string
}

func (e *emptyAnswer) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if which(sql) == e.to {
		return &fakeRows{}, nil // no row at all
	}
	return e.fakeDB.Query(ctx, sql, args...)
}

func TestARegisterThatAnswersNothingCountsAsRefused(t *testing.T) {
	// No row means no "true", and treating that as success would report a table
	// registered that the planner will never substitute for.
	out, err := Reconcile(context.Background(),
		&emptyAnswer{fakeDB: newDB("public"), to: "register"}, "public", []string{"orders"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Registered) != 0 {
		t.Fatalf("claimed to register %v", out.Registered)
	}
	if len(out.Skipped) != 1 {
		t.Fatalf("skipped = %+v, want one", out.Skipped)
	}
}

type badBool struct {
	*fakeDB
	to string
}

func (b *badBool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if which(sql) == b.to {
		return &fakeRows{rows: [][]any{{"not a bool"}}}, nil
	}
	return b.fakeDB.Query(ctx, sql, args...)
}

func TestARegisterThatAnswersSomethingElseIsReported(t *testing.T) {
	_, err := Reconcile(context.Background(),
		&badBool{fakeDB: newDB("public"), to: "register"}, "public", []string{"orders"})
	if err == nil {
		t.Fatal("want an error")
	}
}
