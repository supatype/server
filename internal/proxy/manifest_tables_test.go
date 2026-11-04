package proxy

import (
	"encoding/json"
	"testing"
)

// The `tables` map carries what after hooks need about a table's columns. It travels beside the
// hook map, so it has to load, clone and merge on the same terms, or a managed pod would strip
// columns by a stale list.

const tablesManifest = `{
  "hooks": {"posts": {"afterChange": {"function": "index-post"}}},
  "tables": {"posts": {
    "columns": {"afterChange": "all"},
    "restrictedColumns": ["secret"],
    "primaryKey": ["id"]
  }}
}`

func TestTablesSurviveTheManifestRoundTrip(t *testing.T) {
	m, err := ParseRouteManifestJSON([]byte(tablesManifest))
	if err != nil {
		t.Fatal(err)
	}
	got := m.Tables["posts"]
	if got.Columns["afterChange"] != "all" || got.RestrictedColumns[0] != "secret" || got.PrimaryKey[0] != "id" {
		t.Fatalf("tables entry = %+v", got)
	}
	if len(m.Hooks["posts"]) != 1 {
		t.Fatalf("the tables map must not disturb the hook map: %+v", m.Hooks)
	}
}

func TestAClonesTablesAreItsOwn(t *testing.T) {
	var original RouteManifest
	if err := json.Unmarshal([]byte(tablesManifest), &original); err != nil {
		t.Fatal(err)
	}
	clone := CloneRouteManifest(&original)
	clone.Tables["posts"] = TableMeta{RestrictedColumns: []string{"changed"}}
	clone.Tables["other"] = TableMeta{}
	if original.Tables["posts"].RestrictedColumns[0] != "secret" {
		t.Fatal("writing to a clone's tables changed the original")
	}
	if _, ok := original.Tables["other"]; ok {
		t.Fatal("a table added to a clone appeared in the original")
	}
}

func TestAnOverlaysTablesReplaceTheBase(t *testing.T) {
	base := &RouteManifest{Tables: map[string]TableMeta{
		"posts":  {RestrictedColumns: []string{"old"}},
		"legacy": {},
	}}
	overlay := &RouteManifest{Tables: map[string]TableMeta{"posts": {RestrictedColumns: []string{"secret"}}}}

	MergeRouteManifest(base, overlay)

	if base.Tables["posts"].RestrictedColumns[0] != "secret" {
		t.Fatalf("the overlay's tables were dropped: %+v", base.Tables)
	}
	if _, ok := base.Tables["legacy"]; ok {
		t.Fatal("a table the push no longer lists kept its stale entry")
	}
}

func TestTableLookupIsNilSafe(t *testing.T) {
	var none *RouteManifest
	if _, ok := none.Table("posts"); ok {
		t.Fatal("a nil manifest records no tables")
	}
	if none.PrimaryKey("posts") != nil {
		t.Fatal("a nil manifest has no keys")
	}
	m := &RouteManifest{Tables: map[string]TableMeta{"posts": {PrimaryKey: []string{"id"}}}}
	if _, ok := m.Table("missing"); ok {
		t.Fatal("an unlisted table is not recorded")
	}
	key := m.PrimaryKey("posts")
	if len(key) != 1 || key[0] != "id" {
		t.Fatalf("key = %v", key)
	}
	key[0] = "changed"
	if m.PrimaryKey("posts")[0] != "id" {
		t.Fatal("the key handed out is a copy, so a caller cannot change the manifest")
	}
}
