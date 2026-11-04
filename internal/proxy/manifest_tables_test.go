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
