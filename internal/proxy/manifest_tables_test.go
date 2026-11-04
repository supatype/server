package proxy

import (
	"encoding/json"
	"slices"
	"testing"
)

// The manifest's tables map is read from the JSON `supatype push` writes, beside hooks rather than
// inside it, and the accessors answer for a table it records, one it does not, and no manifest.
func TestTheTablesMap(t *testing.T) {
	var m RouteManifest
	raw := `{"hooks":{"posts":{"beforeChange":{"function":"check"}}},
		"tables":{"posts":{"primaryKey":["org","id"],"restrictedColumns":["secret"],"columns":{"afterChange":"all"}}}}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("a manifest with a tables map failed to load: %v", err)
	}

	meta, ok := m.Table("posts")
	if !ok || meta.Columns["afterChange"] != "all" || !slices.Equal(meta.RestrictedColumns, []string{"secret"}) {
		t.Errorf("posts = %+v, %v", meta, ok)
	}
	if got := m.PrimaryKey("posts"); !slices.Equal(got, []string{"org", "id"}) {
		t.Errorf("primary key = %v", got)
	}
	// A copy, so a caller appending to it cannot change what the next request reads.
	m.PrimaryKey("posts")[0] = "changed"
	if m.Tables["posts"].PrimaryKey[0] != "org" {
		t.Error("PrimaryKey handed out the manifest's own slice")
	}

	if _, ok := m.Table("comments"); ok {
		t.Error("a table the manifest does not record was found")
	}
	if got := m.PrimaryKey("comments"); got != nil {
		t.Errorf("unrecorded table's key = %v, want nil", got)
	}
	var none *RouteManifest
	if got := none.PrimaryKey("posts"); got != nil {
		t.Errorf("nil manifest's key = %v, want nil", got)
	}
}

// A clone shares nothing with the original, so one tenant's reload cannot change what another
// tenant's in-flight request is reading.
func TestCloningTheTablesMap(t *testing.T) {
	original := &RouteManifest{Tables: map[string]TableMeta{"posts": {
		Columns:           map[string]string{"afterChange": "all"},
		RestrictedColumns: []string{"secret"},
		PrimaryKey:        []string{"id"},
	}}}
	clone := CloneRouteManifest(original)

	clone.Tables["posts"].Columns["afterChange"] = "safe"
	clone.Tables["posts"].RestrictedColumns[0] = "changed"
	clone.Tables["posts"].PrimaryKey[0] = "changed"
	clone.Tables["comments"] = TableMeta{}

	got := original.Tables["posts"]
	if got.Columns["afterChange"] != "all" || got.RestrictedColumns[0] != "secret" || got.PrimaryKey[0] != "id" {
		t.Errorf("the clone shared state with the original: %+v", got)
	}
	if _, ok := original.Tables["comments"]; ok {
		t.Error("the clone shared its map with the original")
	}
	if empty := CloneRouteManifest(&RouteManifest{}); empty.Tables != nil {
		t.Errorf("an empty map cloned to %v, want nil", empty.Tables)
	}
}

// The overlay's map replaces the base's wholesale, like hooks: a table dropped from the schema
// must lose its facts, and an overlay that says nothing leaves the base alone.
func TestMergingTheTablesMap(t *testing.T) {
	base := &RouteManifest{Tables: map[string]TableMeta{"old": {PrimaryKey: []string{"id"}}}}

	MergeRouteManifest(base, &RouteManifest{})
	if _, ok := base.Tables["old"]; !ok {
		t.Fatal("an overlay with no tables map cleared the base's")
	}

	MergeRouteManifest(base, &RouteManifest{Tables: map[string]TableMeta{"posts": {PrimaryKey: []string{"id"}}}})
	if _, ok := base.Tables["old"]; ok {
		t.Error("a table the overlay dropped kept its facts")
	}
	if _, ok := base.Tables["posts"]; !ok {
		t.Error("the overlay's table is missing")
	}
}

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
