package modelhooks

import (
	"encoding/json"
	"testing"

	"github.com/supatype/server/internal/proxy"
)

func row(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var r map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("bad fixture %q: %v", raw, err)
	}
	return r
}

func TestStripRemovesRestrictedColumnsByDefault(t *testing.T) {
	shape := TableShape{RestrictedColumns: []string{"salary", "ssn"}}
	got := shape.Strip(EventAfterChange, row(t, `{"id":1,"name":"a","salary":10,"ssn":"x"}`))
	if _, ok := got["salary"]; ok {
		t.Fatalf("salary survived: %v", got)
	}
	if _, ok := got["ssn"]; ok {
		t.Fatalf("ssn survived: %v", got)
	}
	if string(got["name"]) != `"a"` || string(got["id"]) != `1` {
		t.Fatalf("unrestricted columns lost: %v", got)
	}
}

func TestStripKeepsEverythingOnlyForTheEventThatOptedIn(t *testing.T) {
	shape := TableShape{
		Columns:           map[string]string{EventAfterChange: ColumnsAll, EventAfterDelete: "safe"},
		RestrictedColumns: []string{"salary"},
	}
	if got := shape.Strip(EventAfterChange, row(t, `{"id":1,"salary":10}`)); string(got["salary"]) != `10` {
		t.Fatalf("afterChange opted in and should keep salary, got %v", got)
	}
	if got := shape.Strip(EventAfterDelete, row(t, `{"id":1,"salary":10}`)); got["salary"] != nil {
		t.Fatalf("afterDelete did not opt in and must lose salary, got %v", got)
	}
}

func TestStripTreatsAnUnknownColumnsValueAsSafe(t *testing.T) {
	// Only the exact opt-in widens the row. A typo must not leak a column.
	shape := TableShape{Columns: map[string]string{EventAfterChange: "ALL"}, RestrictedColumns: []string{"salary"}}
	got := shape.Strip(EventAfterChange, row(t, `{"id":1,"salary":10}`))
	if _, ok := got["salary"]; ok {
		t.Fatalf("an unrecognised columns value widened the row: %v", got)
	}
}

func TestStripDoesNotMutateItsInput(t *testing.T) {
	in := row(t, `{"id":1,"salary":10}`)
	TableShape{RestrictedColumns: []string{"salary"}}.Strip(EventAfterChange, in)
	if _, ok := in["salary"]; !ok {
		t.Fatal("Strip changed the caller's map")
	}
}

func TestShapesFromManifestReadsTheTablesMap(t *testing.T) {
	m := &proxy.RouteManifest{Tables: map[string]proxy.TableMeta{
		"posts": {Columns: map[string]string{EventAfterChange: ColumnsAll}, RestrictedColumns: []string{"secret"}, PrimaryKey: []string{"id"}},
	}}
	shape, ok := ShapesFromManifest(m)("posts")
	if !ok || shape.Columns[EventAfterChange] != ColumnsAll || shape.RestrictedColumns[0] != "secret" || shape.PrimaryKey[0] != "id" {
		t.Fatalf("shape = %+v, %v", shape, ok)
	}
}

func TestShapesFromManifestLeavesTablesWithoutAnEntryAlone(t *testing.T) {
	// The policy for an older CLI's manifest is an open decision; until it is made, such a table is
	// reported as unknown so every after-hook path keeps the released behaviour for it.
	if _, ok := ShapesFromManifest(&proxy.RouteManifest{})("posts"); ok {
		t.Fatal("a manifest with no tables map must not invent a shape")
	}
	m := &proxy.RouteManifest{Tables: map[string]proxy.TableMeta{"other": {}}}
	if _, ok := ShapesFromManifest(m)("posts"); ok {
		t.Fatal("a table missing from the tables map must not invent a shape")
	}
	if _, ok := ShapesFromManifest(nil)("posts"); ok {
		t.Fatal("no manifest at all must not invent a shape")
	}
}
