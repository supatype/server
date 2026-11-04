package changelog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/supatype/server/internal/modelhooks"
)

// How the listener behaves when the database is not there for it. Same database as live_test.go.

func connect(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// closedSlot is a slot on a connection that has already been closed.
func closedSlot(t *testing.T, dsn string) slot {
	t.Helper()
	conn := connect(t, dsn)
	if err := conn.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	return slot{conn: conn, name: "supatype_hooks_test_closed"}
}

func TestLiveEverySlotCallReportsALostConnection(t *testing.T) {
	s := closedSlot(t, liveDSN(t))
	ctx := context.Background()
	if err := s.ensure(ctx); err == nil {
		t.Error("ensure")
	}
	if _, err := s.tryLock(ctx); err == nil {
		t.Error("tryLock")
	}
	if _, err := s.pushLockHeld(ctx); err == nil {
		t.Error("pushLockHeld")
	}
	if _, err := s.peek(ctx, func([]byte) error { return nil }); err == nil {
		t.Error("peek")
	}
	if err := s.advance(ctx, "0/1"); err == nil {
		t.Error("advance")
	}
}

func TestLiveCreatingASlotThatAnotherReplicaJustMadeIsNotAnError(t *testing.T) {
	dsn := liveDSN(t)
	name := "supatype_hooks_test_race"
	exec(t, dsn, `SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = '`+name+`'`)
	t.Cleanup(func() { exec(t, dsn, `SELECT pg_drop_replication_slot('`+name+`')`) })
	s := slot{conn: connect(t, dsn), name: name}
	if err := s.create(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The second create is the replica that lost the race between its exists check and its create.
	if err := s.create(context.Background()); err != nil {
		t.Fatalf("losing the race must not fail the session: %v", err)
	}
}

func TestLiveWaitsWithoutASlotUntilThereIsWork(t *testing.T) {
	dsn := liveDSN(t)
	l := New(Config{DSN: dsn, Poll: 10 * time.Millisecond, Slot: "supatype_hooks_test_never_made"})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	s := slot{conn: connect(t, dsn), name: l.cfg.Slot}
	if err := l.waitForWork(ctx, s); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("with no after hooks and no slot it waits, and gives up only with ctx: %v", err)
	}
	if err := l.waitForWork(context.Background(), closedSlot(t, dsn)); err == nil {
		t.Fatal("a lost connection while waiting must end the session")
	}
}

func TestLiveAStandbyWaitsForTheLock(t *testing.T) {
	dsn := liveDSN(t)
	holder := slot{conn: connect(t, dsn)}
	if held, err := holder.tryLock(context.Background()); err != nil || !held {
		t.Fatalf("could not take the lock to hold it: %v %v", held, err)
	}
	l := New(Config{DSN: dsn, Poll: 10 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := l.waitForLock(ctx, slot{conn: connect(t, dsn)}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a standby waits while another replica reads: %v", err)
	}
	if err := l.waitForLock(context.Background(), closedSlot(t, dsn)); err == nil {
		t.Fatal("a lost connection while standing by must end the session")
	}
}

func TestLiveReadEndsTheSessionOnAFailedRead(t *testing.T) {
	dsn := liveDSN(t)
	l := New(Config{DSN: dsn, Poll: 10 * time.Millisecond})
	if err := l.read(context.Background(), slot{conn: connect(t, dsn), name: "supatype_hooks_test_missing"}); err == nil {
		t.Fatal("reading a slot that does not exist must end the session")
	}
	if err := l.read(context.Background(), closedSlot(t, dsn)); err == nil {
		t.Fatal("a lost connection must end the session")
	}
}

func TestLiveRunRetriesAnUnreachableDatabase(t *testing.T) {
	l := New(Config{DSN: "postgres://nobody:x@127.0.0.1:1/none?connect_timeout=1", Poll: 10 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	l.Run(ctx)
	st := l.Status()
	if st.Path != PathUnknown || st.LastError == "" {
		t.Fatalf("an unreachable database is transient: path stays unknown, the error is reported: %+v", st)
	}
}

func TestLiveSessionEndsWhenShutdownComesWhileWaiting(t *testing.T) {
	dsn := liveDSN(t)
	// No hooks: the session waits for work until shutdown.
	idle := New(Config{DSN: dsn, Poll: 10 * time.Millisecond, Slot: "supatype_hooks_test_never_made"})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	idle.Run(ctx)
	if idle.Status().LastError != "" {
		t.Fatalf("a shutdown is not an error: %+v", idle.Status())
	}

	// Another replica holds the lock: the session stands by until shutdown.
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	holder := slot{conn: connect(t, dsn)}
	if held, err := holder.tryLock(context.Background()); err != nil || !held {
		t.Fatalf("could not hold the lock: %v %v", held, err)
	}
	standby := New(cfg)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { standby.Run(ctx2); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && standby.Status().Role != "standby" {
		time.Sleep(20 * time.Millisecond)
	}
	cancel2()
	<-done
	if st := standby.Status(); st.Path != PathChangeLog || st.Role != "standby" || st.LastError != "" {
		t.Fatalf("it should have stood by on the change-log path and stopped cleanly: %+v", st)
	}
}

func TestLiveAShutdownMidDeliveryLeavesTheSlotWhereItWas(t *testing.T) {
	// Reads the body first: a server only notices a client that hung up once the body is consumed.
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	t.Cleanup(hang.Close)
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	cfg.Resolve = func(fn string) (string, error) { return hang.URL + "/" + fn, nil }
	exec(t, cfg.DSN, `INSERT INTO public.live_posts VALUES (60, 'pending', 'x')`)

	// The slot was made after the insert, so make it first and write again.
	l := New(cfg)
	s := slot{conn: connect(t, cfg.DSN), name: cfg.Slot}
	if err := s.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	exec(t, cfg.DSN, `INSERT INTO public.live_posts VALUES (61, 'pending', 'x')`)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := l.readBatch(ctx, s); err == nil {
		t.Fatal("a delivery cut short by shutdown must be reported")
	}

	// Nothing was confirmed, so a fresh reader still sees the change.
	var seen bool
	if _, err := s.peek(context.Background(), func(data []byte) error {
		seen = seen || strings.Contains(string(data), `"value":61`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("the undelivered change must still be in the slot")
	}
}

func TestLiveADroppedTableIsDeliveredAsDecoded(t *testing.T) {
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	s := slot{conn: connect(t, cfg.DSN), name: cfg.Slot}
	if err := s.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	exec(t, cfg.DSN,
		`INSERT INTO public.live_posts VALUES (70, 'gone soon', 'x')`,
		`DROP TABLE public.live_posts`,
	)
	if _, err := New(cfg).readBatch(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	calls := w.snapshot()
	if len(calls) != 1 || string(calls[0].Body.Rows[0]["title"]) != `"gone soon"` {
		t.Fatalf("a table dropped before the read still owes its delivery, with decoded values: %+v", calls)
	}
}

func TestLiveHookMapsChangingTheSchema(t *testing.T) {
	// The schema the hook map lives in decides which changes are owed.
	b := New(Config{Schema: func() string { return "app" }, Hooks: func() map[string]modelhooks.TableHooksView { return postsHooks() }}).builder()
	if b.handles("public", "posts") {
		t.Fatal("a manifest schema of app must not claim public.posts")
	}
}

// Under REPLICA IDENTITY DEFAULT an update's old row is just its key. Rendering fills every
// column of the table, and the hook must still see only what the change carried.
func TestLiveRenderingKeepsOnlyTheColumnsTheChangeCarried(t *testing.T) {
	w := newStubWorker(t)
	cfg := liveSetup(t, w)
	exec(t, cfg.DSN, `ALTER TABLE public.live_posts REPLICA IDENTITY DEFAULT`)
	s := slot{conn: connect(t, cfg.DSN), name: cfg.Slot}
	if err := s.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	exec(t, cfg.DSN,
		`INSERT INTO public.live_posts VALUES (80, 'a', 'x')`,
		`UPDATE public.live_posts SET id = 81, title = 'b' WHERE id = 80`,
	)
	if _, err := New(cfg).readBatch(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	var update *sentPayload
	for _, c := range w.snapshot() {
		if c.Body.Operation == "update" {
			body := c.Body
			update = &body
		}
	}
	if update == nil || len(update.Old) != 1 {
		t.Fatalf("no update delivered: %+v", w.snapshot())
	}
	if old := update.Old[0]; len(old) != 1 || string(old["id"]) != "80" {
		t.Fatalf("the old row is the key the change carried and nothing else, got %v", old)
	}
	if string(update.Rows[0]["title"]) != `"b"` {
		t.Fatalf("the new row is whole: %v", update.Rows[0])
	}
}
