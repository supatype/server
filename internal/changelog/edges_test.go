package changelog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/supatype/server/internal/modelhooks"
)

func TestDefaultBackoffDoublesAndCaps(t *testing.T) {
	cases := map[int]time.Duration{0: 500 * time.Millisecond, 1: time.Second, 3: 4 * time.Second, 5: 10 * time.Second, 9: 10 * time.Second}
	for attempt, want := range cases {
		if got := defaultBackoff(attempt); got != want {
			t.Errorf("attempt %d: got %v, want %v", attempt, got, want)
		}
	}
}

func TestNewFillsDefaults(t *testing.T) {
	l := New(Config{})
	if l.cfg.Workers != 8 || l.cfg.Poll != time.Second || l.cfg.Slot != SlotName {
		t.Fatalf("defaults = %+v", l.cfg)
	}
	if st := l.Status(); st.Path != PathUnknown || st.Slot != SlotName || l.Active() {
		t.Fatalf("a new listener has not decided anything: %+v", st)
	}
	b := l.builder()
	if b.schema != "public" || b.hooks != nil {
		t.Fatalf("an unconfigured builder reads public with no hooks: %+v", b)
	}
	if _, known := b.shapes("posts"); known {
		t.Fatal("with no shapes configured every table is unknown")
	}
}

func TestANilListenerReportsUnknown(t *testing.T) {
	var l *Listener
	if l.Status().Path != PathUnknown || l.Active() {
		t.Fatal("a server without a listener must read as not active")
	}
}

func TestTheBuilderUsesTheConfiguredSchema(t *testing.T) {
	l := New(Config{Schema: func() string { return "app" }})
	if b := l.builder(); b.schema != "app" {
		t.Fatalf("schema = %q", b.schema)
	}
}

func TestHandlesOnlyHookedTablesInTheSchema(t *testing.T) {
	b := testBuilder("")
	if b.handles("other_schema", "posts") {
		t.Fatal("a table in another schema is not the hook map's")
	}
	b.hooks["plain"] = modelhooks.TableHooksView{modelhooks.EventBeforeChange: {Function: "only-before"}}
	if b.handles("public", "plain") || b.handles("public", "missing") {
		t.Fatal("a table with no after hook, or none at all, is not the listener's")
	}
	if !b.handles("public", "posts") {
		t.Fatal("posts has after hooks and a shape")
	}
}

func TestAGroupThatCannotBeEncodedIsSkipped(t *testing.T) {
	p := plan{commitLSN: "0/1", groups: []*group{{
		schema: "public", table: "posts", op: "insert", event: modelhooks.EventAfterChange,
		cfg:  modelhooks.HookConfigEntry{Function: "f"},
		rows: []map[string]json.RawMessage{{"id": json.RawMessage("{not json")}},
	}}}
	if ds := p.deliveries(); len(ds) != 0 {
		t.Fatalf("half a payload must not be sent: %+v", ds)
	}
}

func TestAnUpdateWithoutAnOldRowReportsItsKey(t *testing.T) {
	keyOnly := `{"action":"U","lsn":"0/2","schema":"public","table":"posts","columns":[{"name":"id","type":"integer","value":1},{"name":"title","type":"text","value":"b"}],"pk":[{"name":"id","type":"integer"}]}`
	p := decodeSent(t, build(t, fullTxn(t, keyOnly), "")[0])
	if len(p.Old) != 1 || string(p.Old[0]["id"]) != "1" || len(p.Old[0]) != 1 {
		t.Fatalf("without REPLICA IDENTITY FULL the old row is the key alone: %+v", p.Old)
	}
}

func TestUnsupportedErrorNamesTheReasonAndWrapsTheCause(t *testing.T) {
	cause := errors.New("boom")
	err := &UnsupportedError{Reason: "wal_level is not logical", Err: cause}
	if !strings.Contains(err.Error(), "wal_level is not logical") || !errors.Is(err, cause) {
		t.Fatalf("error = %q", err)
	}
}

func TestAssemblerEdges(t *testing.T) {
	asm := newAssembler(hookedPosts)
	if _, err := asm.feed([]byte("not json")); err == nil {
		t.Fatal("an unreadable row is an error")
	}
	if _, err := asm.feed([]byte(recCommit)); err == nil {
		t.Fatal("a commit with nothing open is a stream we lost track of")
	}
	if _, err := asm.feed([]byte(recActor)); err == nil {
		t.Fatal("a transactional message with nothing open is a stream we lost track of")
	}

	truncate := `{"action":"T","lsn":"0/3","schema":"public","table":"posts"}`
	garbled := `{"action":"M","lsn":"0/4","transactional":true,"prefix":"supatype.actor","content":"not json"}`
	txn := onlyTxn(t, feedAll(t, recBegin, garbled, recSkip, truncate, recInsert, recCommit))
	if len(txn.Changes) != 1 {
		t.Fatalf("a truncate is not a row change: %+v", txn.Changes)
	}
	if txn.Actor != nil || txn.Skipped() {
		t.Fatalf("a garbled first actor still counts as an actor, so the skip loses: %+v", txn)
	}
}

func TestDecoderEdges(t *testing.T) {
	if rowOf(nil) != nil {
		t.Fatal("no columns is no row")
	}
	if keyNames(nil) != nil {
		t.Fatal("a table without a key has no key names")
	}
	if got := string(valueOf(column{Type: "text"})); got != "null" {
		t.Fatalf("a missing value is null, got %s", got)
	}
	notJSON := column{Type: "jsonb", Value: json.RawMessage(`"{broken"`)}
	if got := string(valueOf(notJSON)); got != `"{broken"` {
		t.Fatalf("a json value that does not parse is passed through, got %s", got)
	}
	prefixed := column{Type: "bytea", Value: json.RawMessage(`"\\xff"`)}
	if got := string(valueOf(prefixed)); got != `"\\xff"` {
		t.Fatalf("bytea that already has its prefix is left alone, got %s", got)
	}
}

// blockingCaller holds every call until the context ends.
type blockingCaller struct{ started chan struct{} }

func (b blockingCaller) Call(ctx context.Context, _, _ string, _ modelhooks.HookConfigView, _ []byte, _ int) modelhooks.Outcome {
	b.started <- struct{}{}
	<-ctx.Done()
	return modelhooks.Outcome{Kind: modelhooks.OutcomeUnavailable, Reason: "cancelled"}
}

func TestSendAllStopsQueuingWhenTheContextEnds(t *testing.T) {
	c := blockingCaller{started: make(chan struct{}, 1)}
	s := testSender(c, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-c.started; cancel() }()
	if err := s.sendAll(ctx, []Delivery{{Function: "a"}, {Function: "b"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("a delivery still waiting for a worker when shutdown comes must be reported, got %v", err)
	}
}
