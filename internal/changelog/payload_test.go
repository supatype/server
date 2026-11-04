package changelog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/supatype/server/internal/modelhooks"
)

type sentPayload struct {
	Table      string                       `json:"table"`
	Operation  string                       `json:"operation"`
	User       *modelhooks.Claims           `json:"user"`
	RequestID  string                       `json:"requestId"`
	DeliveryID string                       `json:"deliveryId"`
	Rows       []map[string]json.RawMessage `json:"rows"`
	Old        []map[string]json.RawMessage `json:"old"`
}

func decodeSent(t *testing.T, d Delivery) sentPayload {
	t.Helper()
	var p sentPayload
	if err := json.Unmarshal(d.Body, &p); err != nil {
		t.Fatalf("payload is not JSON: %v\n%s", err, d.Body)
	}
	return p
}

func postsHooks() map[string]modelhooks.TableHooksView {
	return map[string]modelhooks.TableHooksView{
		"posts": {
			modelhooks.EventAfterChange: {Function: "index-post", TimeoutMs: 1500},
			modelhooks.EventAfterDelete: {Function: "unindex-post"},
		},
	}
}

func shapesWith(columns string) modelhooks.ShapeFunc {
	return func(table string) (modelhooks.TableShape, bool) {
		if table != "posts" {
			return modelhooks.TableShape{}, false
		}
		return modelhooks.TableShape{Columns: map[string]string{modelhooks.EventAfterChange: columns}, RestrictedColumns: []string{"secret"}, PrimaryKey: []string{"id"}}, true
	}
}

func testBuilder(columns string) builder {
	return builder{schema: "public", hooks: postsHooks(), shapes: shapesWith(columns), newID: func() string { return "generated" }}
}

func build(t *testing.T, txn *Txn, columns string) []Delivery {
	t.Helper()
	return testBuilder(columns).deliveries(txn)
}

func fullTxn(t *testing.T, records ...string) *Txn {
	t.Helper()
	all := append([]string{recBegin}, records...)
	return onlyTxn(t, feedAll(t, append(all, recCommit)...))
}

// A second row in posts, so a transaction can carry two changes of one operation.
const recInsert2 = `{"action":"I","lsn":"0/1A74739","schema":"public","table":"posts","columns":[{"name":"id","type":"integer","value":2},{"name":"title","type":"text","value":"second"},{"name":"secret","type":"text","value":"s2"}],"pk":[{"name":"id","type":"integer"}]}`

func TestOneDeliveryPerTableAndOperation(t *testing.T) {
	ds := build(t, fullTxn(t, recActor, recInsert, recInsert2, recUpdate, recDelete), "")
	if len(ds) != 3 {
		t.Fatalf("want one call each for insert, update and delete, got %d", len(ds))
	}
	ins := decodeSent(t, ds[0])
	if ins.Operation != "insert" || len(ins.Rows) != 2 || ins.Old != nil {
		t.Fatalf("both inserted rows belong in one call: %+v", ins)
	}
	if string(ins.Rows[0]["id"]) != "1" || string(ins.Rows[1]["id"]) != "2" {
		t.Fatalf("rows keep commit order: %+v", ins.Rows)
	}
	if ins.User == nil || ins.User.Sub != "u1" || ins.User.Email != "a@b.c" || ins.RequestID != "req-1" {
		t.Fatalf("identity: %+v", ins)
	}
	if ds[0].Event != modelhooks.EventAfterChange || ds[0].Function != "index-post" || ds[0].View.TimeoutMs != 1500 {
		t.Fatalf("insert routing: %+v", ds[0])
	}

	upd := decodeSent(t, ds[1])
	if upd.Operation != "update" || len(upd.Rows) != 1 || len(upd.Old) != 1 ||
		string(upd.Rows[0]["title"]) != `"b"` || string(upd.Old[0]["title"]) != `"a"` {
		t.Fatalf("update rows and old must align by index: %+v", upd)
	}

	del := decodeSent(t, ds[2])
	if del.Operation != "delete" || del.Rows != nil || len(del.Old) != 1 || string(del.Old[0]["title"]) != `"b"` {
		t.Fatalf("delete payload: %+v", del)
	}
	if ds[2].Event != modelhooks.EventAfterDelete || ds[2].Function != "unindex-post" {
		t.Fatalf("delete routing: %+v", ds[2])
	}
}

func TestDeliveryIDIsCommitTableAndOperation(t *testing.T) {
	ds := build(t, fullTxn(t, recInsert, recInsert2, recDelete), "")
	want := []string{"0/1A749F0:public.posts:insert", "0/1A749F0:public.posts:delete"}
	if len(ds) != len(want) {
		t.Fatalf("got %d deliveries", len(ds))
	}
	for i, d := range ds {
		if d.DeliveryID != want[i] || decodeSent(t, d).DeliveryID != want[i] {
			t.Fatalf("delivery %d id = %q, want %q", i, d.DeliveryID, want[i])
		}
	}
	again := build(t, fullTxn(t, recInsert, recInsert2, recDelete), "")
	if again[0].DeliveryID != ds[0].DeliveryID {
		t.Fatal("a redelivered transaction must carry the same id")
	}
}

func TestDeliveriesStripRestrictedColumnsUnlessOptedIn(t *testing.T) {
	p := decodeSent(t, build(t, fullTxn(t, recUpdate), "")[0])
	if _, ok := p.Rows[0]["secret"]; ok {
		t.Fatalf("restricted column in rows: %v", p.Rows[0])
	}
	if _, ok := p.Old[0]["secret"]; ok {
		t.Fatalf("restricted column in old: %v", p.Old[0])
	}

	p = decodeSent(t, build(t, fullTxn(t, recUpdate), modelhooks.ColumnsAll)[0])
	if string(p.Rows[0]["secret"]) != `"s"` || string(p.Old[0]["secret"]) != `"s"` {
		t.Fatalf("columns: all should keep the restricted column: %+v", p)
	}
}

func TestOptingInAfterChangeDoesNotWidenAfterDelete(t *testing.T) {
	ds := build(t, fullTxn(t, recDelete), modelhooks.ColumnsAll)
	if _, ok := decodeSent(t, ds[0]).Old[0]["secret"]; ok {
		t.Fatal("columns: all on afterChange must not show afterDelete the restricted column")
	}
}

func TestDeliveriesWithoutAnActorHaveNoUserAndAGeneratedRequestID(t *testing.T) {
	p := decodeSent(t, build(t, fullTxn(t, recInsert), "")[0])
	if p.User != nil || p.RequestID != "generated" {
		t.Fatalf("no actor marker must mean user null and a generated id, got %+v %q", p.User, p.RequestID)
	}
}

func TestAnActorWithoutASubjectIsAnonymous(t *testing.T) {
	anon := `{"action":"M","lsn":"0/1","transactional":true,"prefix":"supatype.actor","content":"{\"sub\":null,\"role\":\"anon\",\"requestId\":\"r\"}"}`
	if p := decodeSent(t, build(t, fullTxn(t, anon, recInsert), "")[0]); p.User != nil {
		t.Fatalf("an anon caller is user null, as on the request path; got %+v", p.User)
	}
}

func TestSkippedTransactionsDeliverNothing(t *testing.T) {
	if ds := build(t, fullTxn(t, recSkip, recInsert), ""); len(ds) != 0 {
		t.Fatalf("seed transaction delivered %d", len(ds))
	}
}

func TestDeliveriesSkipOperationsWithoutAHook(t *testing.T) {
	b := testBuilder("")
	b.hooks = map[string]modelhooks.TableHooksView{"posts": {modelhooks.EventAfterDelete: {Function: "unindex-post"}}}
	ds := b.deliveries(fullTxn(t, recInsert, recDelete))
	if len(ds) != 1 || ds[0].Event != modelhooks.EventAfterDelete {
		t.Fatalf("only the delete has a hook: %+v", ds)
	}
}

func TestDeliveryDepthFollowsTheActor(t *testing.T) {
	deep := `{"action":"M","lsn":"0/1","transactional":true,"prefix":"supatype.actor","content":"{\"sub\":\"u\",\"depth\":2}"}`
	if d := build(t, fullTxn(t, deep, recInsert), "")[0]; d.Depth != 3 {
		t.Fatalf("a write made two hooks deep fires its after hook at depth 3, got %d", d.Depth)
	}
	if d := build(t, fullTxn(t, recInsert), "")[0]; d.Depth != 1 {
		t.Fatalf("a write with no actor fires at depth 1, got %d", d.Depth)
	}
}

func actorWithRequestPath(token string) string {
	content, _ := json.Marshal(map[string]string{"sub": "u1", "requestPath": token})
	encoded, _ := json.Marshal(string(content))
	return `{"action":"M","lsn":"0/1","transactional":true,"prefix":"supatype.actor","content":` + string(encoded) + `}`
}

func TestATableTheRequestPathAlreadyFiredIsNotDeliveredAgain(t *testing.T) {
	b := testBuilder("")
	b.hooks["other"] = modelhooks.TableHooksView{modelhooks.EventAfterChange: {Function: "other-fn"}}
	b.shapes = func(string) (modelhooks.TableShape, bool) { return modelhooks.TableShape{}, true }
	b.firedOnRequest = func(token string) (string, bool) { return "posts", token == "genuine" }
	asm := newAssembler(b.handles)
	feed := func(records ...string) *Txn {
		var txn *Txn
		for _, r := range records {
			got, err := asm.feed([]byte(r))
			if err != nil {
				t.Fatal(err)
			}
			if got != nil {
				txn = got
			}
		}
		return txn
	}

	ds := b.deliveries(feed(recBegin, actorWithRequestPath("genuine"), recInsert, recOther, recCommit))
	if len(ds) != 1 || decodeSent(t, ds[0]).Table != "other" {
		t.Fatalf("posts fired on the request path; only the write to other is owed: %+v", ds)
	}

	ds = b.deliveries(feed(recBegin, actorWithRequestPath("forged"), recInsert, recCommit))
	if len(ds) != 1 || decodeSent(t, ds[0]).Table != "posts" {
		t.Fatalf("a token that does not verify must not suppress a delivery: %+v", ds)
	}
}

// fakeRenderer marks rows so the test can see rendering happened, or fails.
type fakeRenderer struct{ fail bool }

func (f fakeRenderer) render(_ context.Context, _, _ string, rows []map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	if f.fail {
		return nil, errors.New("table is gone")
	}
	out := make([]map[string]json.RawMessage, len(rows))
	for i, r := range rows {
		out[i] = map[string]json.RawMessage{"id": r["id"], "rendered": json.RawMessage("true")}
	}
	return out, nil
}

func TestRenderingRewritesRowsAndOldAndKeepsDecodedValuesOnFailure(t *testing.T) {
	b := testBuilder("")
	p := b.plan(fullTxn(t, recUpdate))
	p.render(context.Background(), fakeRenderer{}, func(string, error) { t.Fatal("unexpected render error") })
	got := decodeSent(t, p.deliveries()[0])
	if string(got.Rows[0]["rendered"]) != "true" || string(got.Old[0]["rendered"]) != "true" {
		t.Fatalf("rows and old should both be rendered: %+v", got)
	}

	p = b.plan(fullTxn(t, recUpdate))
	var failures int
	p.render(context.Background(), fakeRenderer{fail: true}, func(string, error) { failures++ })
	got = decodeSent(t, p.deliveries()[0])
	if failures == 0 || string(got.Rows[0]["title"]) != `"b"` {
		t.Fatalf("a render failure keeps the decoded row and is reported: %d %+v", failures, got)
	}
}
