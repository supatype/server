package changelog

import (
	"encoding/json"
	"testing"
)

// Records as wal2json format 2 printed them on supatype/postgres:latest (2026-11-03), with the
// options this package reads with. Kept verbatim so a change in the decoder is tested against what
// the plugin really sends.
const (
	recBegin      = `{"action":"B","lsn":"0/1A749F0","nextlsn":"0/1A74A20"}`
	recActor      = `{"action":"M","lsn":"0/1A74738","transactional":true,"prefix":"supatype.actor","content":"{\"sub\":\"u1\",\"role\":\"authenticated\",\"email\":\"a@b.c\",\"requestId\":\"req-1\"}"}`
	recInsert     = `{"action":"I","lsn":"0/1A74738","schema":"public","table":"posts","columns":[{"name":"id","type":"integer","value":1},{"name":"title","type":"text","value":"a"},{"name":"secret","type":"text","value":"s"}],"pk":[{"name":"id","type":"integer"}]}`
	recOther      = `{"action":"I","lsn":"0/1A74740","schema":"public","table":"other","columns":[{"name":"id","type":"integer","value":1}],"pk":[{"name":"id","type":"integer"}]}`
	recUpdate     = `{"action":"U","lsn":"0/1A748F8","schema":"public","table":"posts","columns":[{"name":"id","type":"integer","value":1},{"name":"title","type":"text","value":"b"},{"name":"secret","type":"text","value":"s"}],"identity":[{"name":"id","type":"integer","value":1},{"name":"title","type":"text","value":"a"},{"name":"secret","type":"text","value":"s"}],"pk":[{"name":"id","type":"integer"}]}`
	recForged     = `{"action":"M","lsn":"0/1A749A8","transactional":true,"prefix":"supatype.actor","content":"{\"sub\":\"forged\"}"}`
	recDelete     = `{"action":"D","lsn":"0/1A749A8","schema":"public","table":"posts","identity":[{"name":"id","type":"integer","value":1},{"name":"title","type":"text","value":"b"},{"name":"secret","type":"text","value":"s"}],"pk":[{"name":"id","type":"integer"}]}`
	recCommit     = `{"action":"C","lsn":"0/1A749F0","nextlsn":"0/1A74A20"}`
	recSkip       = `{"action":"M","lsn":"0/1A74700","transactional":true,"prefix":"supatype.hooks","content":"skip"}`
	recNonTxSkip  = `{"action":"M","lsn":"0/1A74700","transactional":false,"prefix":"supatype.hooks","content":"skip"}`
	recJSONColumn = `{"action":"I","lsn":"0/1","schema":"public","table":"posts","columns":[{"name":"id","type":"bigint","value":9007199254740993},{"name":"meta","type":"jsonb","value":"{\"a\": [1, 2]}"},{"name":"n","type":"numeric","value":1.50}],"pk":[{"name":"id","type":"bigint"}]}`
)

func hookedPosts(schema, table string) bool { return schema == "public" && table == "posts" }

func feedAll(t *testing.T, records ...string) []*Txn {
	t.Helper()
	asm := newAssembler(hookedPosts)
	var done []*Txn
	for _, r := range records {
		txn, err := asm.feed([]byte(r))
		if err != nil {
			t.Fatalf("feed %s: %v", r, err)
		}
		if txn != nil {
			done = append(done, txn)
		}
	}
	return done
}

func onlyTxn(t *testing.T, txns []*Txn) *Txn {
	t.Helper()
	if len(txns) != 1 {
		t.Fatalf("want one transaction, got %d", len(txns))
	}
	return txns[0]
}

func TestAssemblerKeepsOnlyHookedTables(t *testing.T) {
	txn := onlyTxn(t, feedAll(t, recBegin, recActor, recInsert, recOther, recUpdate, recDelete, recCommit))
	if len(txn.Changes) != 3 {
		t.Fatalf("want insert, update, delete on posts; got %d changes", len(txn.Changes))
	}
	for _, c := range txn.Changes {
		if c.Table != "posts" {
			t.Fatalf("unhooked table kept: %s", c.Table)
		}
	}
	if txn.CommitLSN != "0/1A749F0" || txn.EndLSN != "0/1A74A20" {
		t.Fatalf("commit %q end %q", txn.CommitLSN, txn.EndLSN)
	}
}

func TestAssemblerReadsRowsAndOldRows(t *testing.T) {
	txn := onlyTxn(t, feedAll(t, recBegin, recInsert, recUpdate, recDelete, recCommit))
	ins, upd, del := txn.Changes[0], txn.Changes[1], txn.Changes[2]

	if ins.Op != "insert" || string(ins.Row["title"]) != `"a"` || ins.Old != nil {
		t.Fatalf("insert: %+v", ins)
	}
	if upd.Op != "update" || string(upd.Row["title"]) != `"b"` || string(upd.Old["title"]) != `"a"` {
		t.Fatalf("update: %+v", upd)
	}
	if del.Op != "delete" || del.Row != nil || string(del.Old["title"]) != `"b"` {
		t.Fatalf("delete: %+v", del)
	}
	if len(ins.Key) != 1 || ins.Key[0] != "id" {
		t.Fatalf("key columns: %v", ins.Key)
	}
}

func TestAssemblerTrustsOnlyTheFirstActorMarker(t *testing.T) {
	txn := onlyTxn(t, feedAll(t, recBegin, recActor, recInsert, recForged, recDelete, recCommit))
	if txn.Actor == nil || txn.Actor.Sub != "u1" {
		t.Fatalf("want the first actor u1, got %+v", txn.Actor)
	}
	if txn.Actor.RequestID != "req-1" || txn.Actor.Email != "a@b.c" || txn.Actor.Role != "authenticated" {
		t.Fatalf("actor fields: %+v", txn.Actor)
	}
}

func TestAssemblerHonoursSkipOnlyWithoutAnActor(t *testing.T) {
	seed := onlyTxn(t, feedAll(t, recBegin, recSkip, recInsert, recCommit))
	if !seed.Skipped() {
		t.Fatal("a seed transaction with a skip marker and no actor must be skipped")
	}

	api := onlyTxn(t, feedAll(t, recBegin, recActor, recSkip, recInsert, recCommit))
	if api.Skipped() {
		t.Fatal("an API transaction must not be able to skip its own hooks")
	}

	// A skip emitted before a forged actor still sits in a transaction that carries an actor, and
	// the contract says skip is honoured only where there is none.
	late := onlyTxn(t, feedAll(t, recBegin, recSkip, recForged, recInsert, recCommit))
	if late.Skipped() {
		t.Fatal("skip must not be honoured in a transaction that carries any actor marker")
	}
}

func TestAssemblerIgnoresNonTransactionalMarkers(t *testing.T) {
	txn := onlyTxn(t, feedAll(t, recBegin, recNonTxSkip, recInsert, recCommit))
	if txn.Skipped() {
		t.Fatal("a non-transactional message is not part of the transaction and must not skip it")
	}
}

func TestAssemblerEmbedsJSONColumnsAndKeepsNumbersExact(t *testing.T) {
	txn := onlyTxn(t, feedAll(t, recBegin, recJSONColumn, recCommit))
	r := txn.Changes[0].Row
	if string(r["id"]) != "9007199254740993" {
		t.Fatalf("bigint lost precision: %s", r["id"])
	}
	if string(r["n"]) != "1.50" {
		t.Fatalf("numeric scale lost: %s", r["n"])
	}
	var meta map[string][]int
	if err := json.Unmarshal(r["meta"], &meta); err != nil || len(meta["a"]) != 2 {
		t.Fatalf("jsonb should arrive as JSON, not a string: %s (%v)", r["meta"], err)
	}
}

func TestAssemblerRejectsAChangeOutsideATransaction(t *testing.T) {
	asm := newAssembler(hookedPosts)
	if _, err := asm.feed([]byte(recInsert)); err == nil {
		t.Fatal("a change with no begin is a stream we have lost track of, and must be an error")
	}
}

func TestAssemblerRestoresTheByteaPrefix(t *testing.T) {
	rec := `{"action":"I","lsn":"0/1","schema":"public","table":"posts","columns":[{"name":"id","type":"integer","value":1},{"name":"by","type":"bytea","value":"deadbeef"}],"pk":[{"name":"id","type":"integer"}]}`
	txn := onlyTxn(t, feedAll(t, recBegin, rec, recCommit))
	if got := string(txn.Changes[0].Row["by"]); got != `"\\xdeadbeef"` {
		t.Fatalf("bytea should read back as the same bytes, got %s", got)
	}
}
