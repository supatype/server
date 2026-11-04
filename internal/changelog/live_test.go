package changelog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/supatype/server/internal/modelhooks"
)

// These run against a real Postgres with wal_level=logical and wal2json allowed, named by
// SUPATYPE_TEST_LOGICAL_DSN as a superuser, for example a throwaway supatype/postgres:latest
// started with `-c wal_level=logical -c output_plugin_libraries=wal2json`. Skipped without it.

type received struct {
	Event string
	Depth string
	Body  sentPayload
}

// stubWorker records every hook call it gets.
type stubWorker struct {
	mu    sync.Mutex
	calls []received
	srv   *httptest.Server
}

func newStubWorker(t *testing.T) *stubWorker {
	w := &stubWorker{}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var p sentPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Errorf("worker got non-JSON: %s", raw)
		}
		w.mu.Lock()
		w.calls = append(w.calls, received{Event: req.Header.Get("X-Supatype-Hook"), Depth: req.Header.Get(modelhooks.HookDepthHeader), Body: p})
		w.mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(w.srv.Close)
	return w
}

func (w *stubWorker) snapshot() []received {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]received(nil), w.calls...)
}

// waitFor polls until n calls arrived, then waits a little longer to catch any extra.
func (w *stubWorker) waitFor(t *testing.T, n int) []received {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && len(w.snapshot()) < n {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	return w.snapshot()
}

// liveDSN is a superuser DSN for a Postgres with logical decoding and wal2json allowed. CI's
// supatype/postgres service (SUPATYPE_SECSQL_DSN) is one, so the listener runs there too.
func liveDSN(t *testing.T) string {
	for _, name := range []string{"SUPATYPE_TEST_LOGICAL_DSN", "SUPATYPE_SECSQL_DSN"} {
		if dsn := strings.TrimSpace(os.Getenv(name)); dsn != "" {
			return dsn
		}
	}
	t.Skip("set SUPATYPE_TEST_LOGICAL_DSN (or SUPATYPE_SECSQL_DSN) to a Postgres with logical decoding")
	return ""
}

func exec(t *testing.T, dsn string, sql ...string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, s := range sql {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// liveSetup makes a fresh hooked table and returns a listener config pointed at a fresh slot.
func liveSetup(t *testing.T, w *stubWorker) Config {
	dsn := liveDSN(t)
	slotName := "supatype_hooks_test_" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_"))
	exec(t, dsn,
		`DROP TABLE IF EXISTS public.live_posts`,
		`CREATE TABLE public.live_posts (id int PRIMARY KEY, title text, secret text)`,
		`ALTER TABLE public.live_posts REPLICA IDENTITY FULL`,
		`SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = '`+slotName+`'`,
	)
	t.Cleanup(func() {
		exec(t, dsn, `SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = '`+slotName+`' AND NOT active`)
	})
	hooks := map[string]modelhooks.TableHooksView{"live_posts": {
		modelhooks.EventAfterChange: {Function: "on-change", TimeoutMs: 2000},
		modelhooks.EventAfterDelete: {Function: "on-delete", TimeoutMs: 2000},
	}}
	shapes := modelhooks.ShapeFunc(func(table string) (modelhooks.TableShape, bool) {
		return modelhooks.TableShape{RestrictedColumns: []string{"secret"}, PrimaryKey: []string{"id"}}, table == "live_posts"
	})
	return Config{
		DSN:        dsn,
		Hooks:      func() map[string]modelhooks.TableHooksView { return hooks },
		Shapes:     func() modelhooks.ShapeFunc { return shapes },
		Resolve:    func(fn string) (string, error) { return w.srv.URL + "/" + fn, nil },
		Dispatcher: modelhooks.NewDispatcher(nil, "test-secret"),
		Poll:       100 * time.Millisecond,
		Slot:       slotName,
	}
}

// start runs a listener until the returned stop is called, and waits for it to read.
func start(t *testing.T, cfg Config) (*Listener, func()) {
	t.Helper()
	l := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && l.Status().Role != "reader" {
		time.Sleep(20 * time.Millisecond)
	}
	if l.Status().Role != "reader" {
		cancel()
		t.Fatalf("listener never became the reader: %+v", l.Status())
	}
	return l, func() { cancel(); <-done }
}

const actorSQL = `SELECT pg_logical_emit_message(true, 'supatype.actor', '{"sub":"user-1","role":"authenticated","email":"u@x.io","requestId":"req-9"}')`

func TestLiveDeliversInsertUpdateDeleteWithTheFirstActor(t *testing.T) {
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	_, stop := start(t, cfg)
	defer stop()

	exec(t, cfg.DSN,
		`BEGIN`,
		actorSQL,
		`INSERT INTO public.live_posts VALUES (1, 'a', 'hidden')`,
		`UPDATE public.live_posts SET title = 'b' WHERE id = 1`,
		`SELECT pg_logical_emit_message(true, 'supatype.actor', '{"sub":"forged"}')`,
		`DELETE FROM public.live_posts WHERE id = 1`,
		`COMMIT`,
	)

	calls := w.waitFor(t, 3)
	if len(calls) != 3 {
		t.Fatalf("want 3 deliveries, got %d: %+v", len(calls), calls)
	}
	ops := []string{"insert", "update", "delete"}
	events := []string{modelhooks.EventAfterChange, modelhooks.EventAfterChange, modelhooks.EventAfterDelete}
	byOp := map[string]received{}
	for _, c := range calls {
		byOp[c.Body.Operation] = c
	}
	for i, op := range ops {
		c, ok := byOp[op]
		if !ok {
			t.Fatalf("no %s delivery: %+v", op, calls)
		}
		if c.Event != events[i] || c.Depth != "1" {
			t.Fatalf("%s: event %q depth %q", op, c.Event, c.Depth)
		}
		if c.Body.User == nil || c.Body.User.Sub != "user-1" || c.Body.RequestID != "req-9" {
			t.Fatalf("%s: the forged second marker must be ignored; user %+v request %q", op, c.Body.User, c.Body.RequestID)
		}
		for _, r := range append(c.Body.Rows, c.Body.Old...) {
			if _, leaked := r["secret"]; leaked {
				t.Fatalf("%s: restricted column reached the hook: %v", op, r)
			}
		}
	}
	if string(byOp["update"].Body.Rows[0]["title"]) != `"b"` || string(byOp["update"].Body.Old[0]["title"]) != `"a"` {
		t.Fatalf("update rows/old: %+v", byOp["update"].Body)
	}
	if string(byOp["delete"].Body.Old[0]["title"]) != `"b"` || byOp["delete"].Body.Rows != nil {
		t.Fatalf("delete old: %+v", byOp["delete"].Body)
	}
}

func TestLiveSkipsASeedTransaction(t *testing.T) {
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	_, stop := start(t, cfg)
	defer stop()

	exec(t, cfg.DSN,
		`BEGIN`,
		`SELECT pg_logical_emit_message(true, 'supatype.hooks', 'skip')`,
		`INSERT INTO public.live_posts VALUES (10, 'seeded', 'x')`,
		`COMMIT`,
		// A marker after the write is user code, and the skip still loses to it.
		`BEGIN`,
		actorSQL,
		`SELECT pg_logical_emit_message(true, 'supatype.hooks', 'skip')`,
		`INSERT INTO public.live_posts VALUES (11, 'api', 'x')`,
		`COMMIT`,
	)

	calls := w.waitFor(t, 1)
	if len(calls) != 1 || string(calls[0].Body.Rows[0]["id"]) != "11" {
		t.Fatalf("want only the API insert delivered, got %+v", calls)
	}
}

func TestLiveResumesFromTheSlotAfterARestart(t *testing.T) {
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	_, stop := start(t, cfg)
	exec(t, cfg.DSN, `INSERT INTO public.live_posts VALUES (20, 'before', 'x')`)
	if got := w.waitFor(t, 1); len(got) != 1 {
		t.Fatalf("first delivery: %+v", got)
	}
	stop()

	// Written while nothing reads, as on a project scaled to zero.
	exec(t, cfg.DSN, `INSERT INTO public.live_posts VALUES (21, 'while-down', 'x')`)

	_, stop = start(t, cfg)
	defer stop()
	calls := w.waitFor(t, 2)
	if len(calls) != 2 || string(calls[1].Body.Rows[0]["id"]) != "21" {
		t.Fatalf("want the write made while down delivered once, and nothing repeated: %+v", calls)
	}
}

func TestLiveOneReaderAndAStandbyTakesOver(t *testing.T) {
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	_, stopFirst := start(t, cfg)

	second := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { second.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	time.Sleep(time.Second)
	if st := second.Status(); st.Role != "standby" || st.Path != PathChangeLog {
		t.Fatalf("second replica should stand by on the change-log path, got %+v", st)
	}

	stopFirst()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && second.Status().Role != "reader" {
		time.Sleep(50 * time.Millisecond)
	}
	if second.Status().Role != "reader" {
		t.Fatalf("standby never took over: %+v", second.Status())
	}
	exec(t, cfg.DSN, `INSERT INTO public.live_posts VALUES (30, 'after-takeover', 'x')`)
	if calls := w.waitFor(t, 1); len(calls) != 1 {
		t.Fatalf("the new reader should deliver: %+v", calls)
	}
}

func TestLiveReportsTheRequestPathWithoutReplicationPrivilege(t *testing.T) {
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	exec(t, cfg.DSN,
		`DROP ROLE IF EXISTS hooks_norepl`,
		`CREATE ROLE hooks_norepl LOGIN PASSWORD 'pw' NOREPLICATION`,
	)
	cfg.DSN = withUser(t, cfg.DSN, "hooks_norepl", "pw")

	l := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	l.Run(ctx) // returns once it knows the database cannot do it
	st := l.Status()
	if st.Path != PathRequest || st.Reason != "the database role may not use replication slots" || l.Active() {
		t.Fatalf("want the request path with a reason, got %+v", st)
	}
}

// withUser is dsn logging in as another role, for postgres:// and postgresql:// URLs alike.
func withUser(t *testing.T, dsn, user, password string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN is not a URL: %v", err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

// Through a real PostgREST whose db-pre-request is auth.emit_hook_actor (the migration
// 20261103180000), named by SUPATYPE_TEST_LOGICAL_POSTGREST with its JWT secret in
// SUPATYPE_TEST_JWT_SECRET. Proves the marker the function writes is the one the listener reads.
func TestLiveThroughPostgRESTCarriesTheCaller(t *testing.T) {
	rest, secret := os.Getenv("SUPATYPE_TEST_LOGICAL_POSTGREST"), os.Getenv("SUPATYPE_TEST_JWT_SECRET")
	if rest == "" || secret == "" {
		t.Skip("set SUPATYPE_TEST_LOGICAL_POSTGREST and SUPATYPE_TEST_JWT_SECRET")
	}
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	exec(t, cfg.DSN,
		`GRANT ALL ON public.live_posts TO anon, authenticated`,
		`NOTIFY pgrst, 'reload schema'`,
	)
	time.Sleep(time.Second)
	_, stop := start(t, cfg)
	defer stop()

	token := signedToken(t, secret)
	call := func(method, path, body string) {
		req, _ := http.NewRequest(method, rest+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-Id", "rest-req-1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode >= 300 {
			t.Fatalf("%s %s answered %d", method, path, res.StatusCode)
		}
	}
	// No Prefer header: the case that used to give an after hook no rows at all.
	call(http.MethodPost, "/live_posts", `{"id":40,"title":"via api","secret":"s"}`)
	call(http.MethodGet, "/live_posts?id=eq.40", "")
	call(http.MethodPatch, "/live_posts?id=eq.40", `{"title":"patched"}`)
	call(http.MethodDelete, "/live_posts?id=eq.40", "")

	calls := w.waitFor(t, 3)
	if len(calls) != 3 {
		t.Fatalf("want insert, update, delete and nothing for the GET; got %+v", calls)
	}
	for _, c := range calls {
		if c.Body.User == nil || c.Body.User.Sub != "api-user" || c.Body.User.Email != "api@x.io" || c.Body.RequestID != "rest-req-1" {
			t.Fatalf("%s: caller not carried: %+v %q", c.Body.Operation, c.Body.User, c.Body.RequestID)
		}
	}
	// Delivered on a pool, so in no particular order.
	insert := byOperation(t, calls, "insert")
	if len(insert.Rows) != 1 || string(insert.Rows[0]["title"]) != `"via api"` {
		t.Fatalf("insert rows without return=representation: %+v", insert)
	}
	if _, leaked := insert.Rows[0]["secret"]; leaked {
		t.Fatal("restricted column reached the hook through the API")
	}
}

func signedToken(t *testing.T, secret string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "api-user", "role": "authenticated", "email": "api@x.io",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// actorMigration is the migration that installs the pre-request function.
const actorMigration = "../../migrations/20261103180000_add_hook_actor_marker.up.sql"

// installActorFunction applies the migration as the migration runner would, with the auth schema
// as its namespace, so the test exercises the SQL that ships.
func installActorFunction(t *testing.T, dsn string) {
	t.Helper()
	raw, err := os.ReadFile(actorMigration)
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ReplaceAll(string(raw), `{{ index .Options "Namespace" }}`, "auth")
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.PgConn().Exec(ctx, "CREATE SCHEMA IF NOT EXISTS auth;\n"+sql).ReadAll(); err != nil {
		t.Fatalf("applying %s: %v", actorMigration, err)
	}
}

// The pre-request function from migration 20261103180000, called the way PostgREST calls it.
func TestLiveActorFunctionMarksWritesAndSkipsReads(t *testing.T) {
	dsn := liveDSN(t)
	installActorFunction(t, dsn)
	slotName := "supatype_hooks_test_actor_fn"
	exec(t, dsn, `SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = '`+slotName+`'`,
		`SELECT pg_create_logical_replication_slot('`+slotName+`', 'wal2json')`)
	t.Cleanup(func() { exec(t, dsn, `SELECT pg_drop_replication_slot('`+slotName+`')`) })

	request := func(method string) []string {
		return []string{
			`BEGIN`,
			`SELECT set_config('request.method', '` + method + `', true)`,
			`SELECT set_config('request.jwt.claims', '{"sub":"s-1","role":"authenticated","email":"e@x.io"}', true)`,
			`SELECT set_config('request.headers', '{"x-request-id":"r-1","x-supatype-hook-depth":"2"}', true)`,
			`SELECT auth.emit_hook_actor()`,
			`COMMIT`,
		}
	}
	exec(t, dsn, append(request("GET"), request("POST")...)...)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var actors []Actor
	_, err = slot{conn: conn, name: slotName}.peek(ctx, func(data []byte) error {
		var rec record
		if err := json.Unmarshal(data, &rec); err != nil {
			return err
		}
		if rec.Action == "M" && rec.Prefix == PrefixActor && rec.Transactional {
			var a Actor
			if err := json.Unmarshal([]byte(rec.Content), &a); err != nil {
				return err
			}
			actors = append(actors, a)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(actors) != 1 {
		t.Fatalf("want one marker, for the POST only; got %+v", actors)
	}
	a := actors[0]
	if a.Sub != "s-1" || a.Role != "authenticated" || a.Email != "e@x.io" || a.RequestID != "r-1" || a.Depth != 2 {
		t.Fatalf("marker = %+v", a)
	}
}

func byOperation(t *testing.T, calls []received, op string) sentPayload {
	t.Helper()
	for _, c := range calls {
		if c.Body.Operation == op {
			return c.Body
		}
	}
	t.Fatalf("no %s delivery in %+v", op, calls)
	return sentPayload{}
}

// Every value a hook receives must be what PostgREST returns for the same row. Checked against a
// real PostgREST reading the row back, type by type.
func TestLiveValuesMatchWhatPostgRESTReturns(t *testing.T) {
	rest := os.Getenv("SUPATYPE_TEST_LOGICAL_POSTGREST")
	if rest == "" {
		t.Skip("set SUPATYPE_TEST_LOGICAL_POSTGREST")
	}
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	exec(t, cfg.DSN, typesTableSQL()...)
	hooks := map[string]modelhooks.TableHooksView{"live_types": {modelhooks.EventAfterChange: {Function: "on-types"}}}
	cfg.Hooks = func() map[string]modelhooks.TableHooksView { return hooks }
	cfg.Shapes = func() modelhooks.ShapeFunc {
		return func(string) (modelhooks.TableShape, bool) { return modelhooks.TableShape{}, true }
	}
	time.Sleep(time.Second)
	_, stop := start(t, cfg)
	defer stop()

	exec(t, cfg.DSN, typesInsertSQL)
	calls := w.waitFor(t, 1)
	if len(calls) != 1 {
		t.Fatalf("want one delivery, got %+v", calls)
	}
	got := calls[0].Body.Rows[0]
	want := postgrestRow(t, rest+"/live_types?id=eq.9007199254740993")
	if len(got) != len(want) {
		t.Fatalf("column sets differ:\nhook %v\nrest %v", keysOf(got), keysOf(want))
	}
	for name, value := range want {
		if compact(t, got[name]) != compact(t, value) {
			t.Errorf("%s: hook got %s, PostgREST returns %s", name, got[name], value)
		}
	}
}

func typesTableSQL() []string {
	return []string{
		`DROP TABLE IF EXISTS public.live_types`,
		`DROP TYPE IF EXISTS public.live_pair`,
		`DROP TYPE IF EXISTS public.live_mood`,
		`CREATE TYPE public.live_mood AS ENUM ('happy', 'sad')`,
		`CREATE TYPE public.live_pair AS (a int, b text)`,
		`CREATE TABLE public.live_types (id bigint PRIMARY KEY, ts timestamptz, tsn timestamp,
			d date, tm time, tmz timetz, iv interval, arr int[], tarr text[], nested int[][], j jsonb,
			jstr jsonb, js json, n numeric, nbig numeric, f float8, r real, by bytea, u uuid,
			m public.live_mood, p public.live_pair, marr public.live_mood[], b bool, nul text,
			rng int4range, ip inet, money_v money, ch char(3), vc varchar(5), bits bit(4), pt point)`,
		`ALTER TABLE public.live_types REPLICA IDENTITY FULL`,
		`GRANT SELECT ON public.live_types TO anon`,
		`NOTIFY pgrst, 'reload schema'`,
	}
}

const typesInsertSQL = `INSERT INTO public.live_types VALUES (9007199254740993,
	'2026-11-03 16:52:08.804824+00', '2026-11-03 16:52:08', '2026-11-03', '12:34:56', '12:34:56+02',
	'1 day 2 hours', '{1,2}', ARRAY['a,b', 'c"d', NULL], '{{1,2},{3,4}}', '{"a":[1,2],"b":"x"}',
	'"str"', '{"x": 1}', 1.50, 123456789012345678901234567890.123456789, 1.5, 0.1,
	'\xdeadbeef', 'a46eff5c-5caa-4532-9ea8-68a4aaecb582', 'happy', ROW(1, 'x,y'), '{happy,sad}',
	true, NULL, '[1,5)', '10.0.0.1/32', 12.34, 'ab', 'abc', B'1010', '(1,2)')`

func postgrestRow(t *testing.T, url string) map[string]json.RawMessage {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("PostgREST answered %d: %s", res.StatusCode, raw)
	}
	return rows[0]
}

func compact(t *testing.T, v json.RawMessage) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, v); err != nil {
		t.Fatalf("not JSON: %s", v)
	}
	return buf.String()
}

func keysOf(row map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	return keys
}

// A write whose after hook the request path already fired carries a signed mark; the listener
// must not deliver it again. A forged mark must not stop a delivery.
func TestLiveARequestPathMarkPreventsASecondDelivery(t *testing.T) {
	rest, secret := os.Getenv("SUPATYPE_TEST_LOGICAL_POSTGREST"), os.Getenv("SUPATYPE_TEST_JWT_SECRET")
	if rest == "" || secret == "" {
		t.Skip("set SUPATYPE_TEST_LOGICAL_POSTGREST and SUPATYPE_TEST_JWT_SECRET")
	}
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	marks := modelhooks.NewRequestPathMarks("server-secret")
	cfg.FiredOnRequest = marks.Verify
	exec(t, cfg.DSN, `GRANT ALL ON public.live_posts TO anon, authenticated`, `NOTIFY pgrst, 'reload schema'`)
	time.Sleep(time.Second)
	_, stop := start(t, cfg)
	defer stop()

	post := func(id int, mark string) {
		req, _ := http.NewRequest(http.MethodPost, rest+"/live_posts", strings.NewReader(fmt.Sprintf(`{"id":%d,"title":"t"}`, id)))
		req.Header.Set("Authorization", "Bearer "+signedToken(t, secret))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(modelhooks.RequestPathHeader, mark)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
	}
	post(50, marks.Mint("live_posts"))
	post(51, modelhooks.NewRequestPathMarks("attacker").Mint("live_posts"))

	calls := w.waitFor(t, 1)
	if len(calls) != 1 || string(calls[0].Body.Rows[0]["id"]) != "51" {
		t.Fatalf("want only the forged-mark write delivered, got %+v", calls)
	}
}
