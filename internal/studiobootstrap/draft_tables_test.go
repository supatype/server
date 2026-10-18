package studiobootstrap

import (
	"reflect"
	"testing"
)

// The drafts classification reads `options.versions` as the engine does.
func TestDraftTablesFromAST(t *testing.T) {
	ast := []byte(`{"models":[
		{"name":"Post","annotations":{"db":{"tableName":"posts"}},"options":{"versions":{"drafts":true,"keep":5}}},
		{"name":"Page","options":{"versions":true}},
		{"name":"Note","annotations":{"db":{"tableName":"notes"}},"options":{"versions":{"keep":5}}},
		{"name":"Ledger","annotations":{"db":{"tableName":"ledger"}},"options":{"versions":{"drafts":false,"keep":5}}},
		{"name":"Tag","annotations":{"db":{"tableName":"tags"}},"options":{"timestamps":true}},
		{"name":"Off","options":{"versions":false}}
	]}`)

	got, err := draftTablesFromAST(ast)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"posts": true, "Page": true, "notes": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDraftTablesFromAnUnreadableAST(t *testing.T) {
	if _, err := draftTablesFromAST([]byte(`{"models":"not a list"}`)); err == nil {
		t.Fatal("an unreadable AST was classified")
	}
}

// Read from the snapshot the last push wrote, and memoised like the other classifications.
func TestDraftTables(t *testing.T) {
	ctx, resources := snapshotStore(t, `{"models":[
		{"name":"Post","annotations":{"db":{"tableName":"posts"}},"options":{"versions":{"drafts":true,"keep":5}}},
		{"name":"Tag","annotations":{"db":{"tableName":"tags"}}}
	]}`)

	tables, ok := DraftTables(ctx, resources)
	if !ok || !tables["posts"] || tables["tags"] {
		t.Fatalf("got (%v, %v), want posts alone", tables, ok)
	}
}

func TestDraftTablesWithNothingToRead(t *testing.T) {
	ctx, resources := snapshotStore(t, "")
	if tables, ok := DraftTables(ctx, resources); ok || tables != nil {
		t.Fatalf("got (%v, %v), want no answer", tables, ok)
	}
}
