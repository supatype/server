package proxy

import (
	"encoding/json"
	"testing"
)

// The cache declaration is a ceiling the schema sets and the runtime may only narrow. Two
// properties carry that, and both are easy to lose without noticing:
//
//   - silence and an explicit false are different instructions, which is why every field is a
//     pointer. Silence defers to the runtime default; false is an opt-out no runtime edit undoes.
//   - a table dropped from the declaration stops being cacheable at once, which is why an overlay
//     replaces the map wholesale instead of merging per table.

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

func TestTableCacheKeepsSilenceAndFalseApart(t *testing.T) {
	// Collapsed into a bare bool these two decode identically, and the hard opt-out quietly
	// becomes a suggestion the runtime is free to ignore.
	var said, silent TableCache
	if err := json.Unmarshal([]byte(`{"enabled":false,"rows":false}`), &said); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{}`), &silent); err != nil {
		t.Fatal(err)
	}

	if said.Enabled == nil || *said.Enabled != false {
		t.Fatalf("explicit false lost: %+v", said.Enabled)
	}
	if said.Rows == nil || *said.Rows != false {
		t.Fatalf("explicit rows:false lost: %+v", said.Rows)
	}
	if silent.Enabled != nil || silent.Rows != nil {
		t.Fatalf("silence became a value: %+v", silent)
	}
}

func TestTableCacheRoundTripsEveryField(t *testing.T) {
	in := TableCache{Enabled: boolPtr(true), MaxTTL: intPtr(60), Public: boolPtr(true), Rows: boolPtr(true)}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out TableCache
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2]any{
		"enabled": {in.Enabled, out.Enabled},
		"public":  {in.Public, out.Public},
		"rows":    {in.Rows, out.Rows},
	} {
		got := pair[1].(*bool)
		want := pair[0].(*bool)
		if got == nil || *got != *want {
			t.Errorf("%s did not round trip: %v", name, got)
		}
	}
	if out.MaxTTL == nil || *out.MaxTTL != 60 {
		t.Errorf("maxTtl did not round trip: %v", out.MaxTTL)
	}
}

func TestTableCacheKeepsAZeroTTL(t *testing.T) {
	// omitempty on a *int keeps zero, where a bare int would drop it — and 0 here means "cache
	// nothing", which is the opposite of "no opinion".
	in := TableCache{MaxTTL: intPtr(0)}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"maxTtl":0}` {
		t.Fatalf("marshalled %s, want a zero that survives", data)
	}
}

func TestAManifestWithNoCacheHasNoEntries(t *testing.T) {
	// Absence is meaningful: a table not in the map cannot be cached by anyone. That has to be the
	// state after parsing a manifest written before this field existed.
	m, err := ParseRouteManifestJSON([]byte(`{"schema":"public"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Cache) != 0 {
		t.Fatalf("cache = %+v, want empty", m.Cache)
	}
}

func TestCloneCopiesTheCacheMap(t *testing.T) {
	// A shared map lets one tenant's reload mutate what another tenant's in-flight request reads.
	orig := &RouteManifest{Cache: map[string]TableCache{"posts": {Enabled: boolPtr(true)}}}
	clone := CloneRouteManifest(orig)

	clone.Cache["posts"] = TableCache{Enabled: boolPtr(false)}
	clone.Cache["added"] = TableCache{}

	if got := orig.Cache["posts"]; got.Enabled == nil || !*got.Enabled {
		t.Errorf("the original's entry was mutated through the clone: %+v", got.Enabled)
	}
	if _, ok := orig.Cache["added"]; ok {
		t.Error("the clone's insert reached the original")
	}
}

func TestAnOverlayReplacesTheCeilingRatherThanMergingIt(t *testing.T) {
	// The property that matters: `legacy` was permitted, the schema no longer declares it, and it
	// must stop being cacheable. A per-table merge would leave the old permission standing.
	base := &RouteManifest{Cache: map[string]TableCache{
		"posts":  {Enabled: boolPtr(true)},
		"legacy": {Enabled: boolPtr(true)},
	}}
	overlay := &RouteManifest{Cache: map[string]TableCache{"posts": {Enabled: boolPtr(true)}}}

	MergeRouteManifest(base, overlay)

	if _, still := base.Cache["legacy"]; still {
		t.Error("a table dropped from the declaration is still permitted")
	}
	if len(base.Cache) != 1 {
		t.Fatalf("cache = %+v, want only posts", base.Cache)
	}
}

func TestAnOverlayWithNoCacheLeavesTheCeilingAlone(t *testing.T) {
	// nil is "this overlay says nothing about caching", not "permit nothing". An overlay that
	// carries only a URL override must not silently disable every cache on the stack.
	base := &RouteManifest{Cache: map[string]TableCache{"posts": {Enabled: boolPtr(true)}}}
	MergeRouteManifest(base, &RouteManifest{PostgRESTURL: "http://example"})

	if len(base.Cache) != 1 {
		t.Fatalf("cache = %+v, want it untouched", base.Cache)
	}
}

func TestAnEmptyOverlayMapClearsTheCeiling(t *testing.T) {
	// Distinct from nil above: a declaration that now permits nothing is an empty map, and it has
	// to be able to say so.
	base := &RouteManifest{Cache: map[string]TableCache{"posts": {Enabled: boolPtr(true)}}}
	MergeRouteManifest(base, &RouteManifest{Cache: map[string]TableCache{}})

	if len(base.Cache) != 0 {
		t.Fatalf("cache = %+v, want cleared", base.Cache)
	}
}

// The wire format, pinned against what the CLI actually produces.
//
// This string is the verbatim output of `manifestCache()` in
// supatype/packages/cli/src/model-cache.ts, run over a schema declaring all four settings, an
// explicit false, and a zero TTL. The two ends live in different repositories and cannot share a
// file, so the defence against them drifting apart is that one of them holds a real sample of the
// other's output and asserts what it means — not just that it parses.
//
// Regenerate by running that emitter, not by editing this by hand.
const cliEmittedManifest = `{"schema":"public","cache":{` +
	`"posts":{"enabled":true,"public":true,"rows":true,"maxTtl":60},` +
	`"drafts":{"enabled":false},` +
	`"zero":{"maxTtl":0}}}`

func TestParsesWhatTheCLIActuallyEmits(t *testing.T) {
	m, err := ParseRouteManifestJSON([]byte(cliEmittedManifest))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Cache) != 3 {
		t.Fatalf("cache = %+v, want three tables", m.Cache)
	}

	posts := m.Cache["posts"]
	if posts.Enabled == nil || !*posts.Enabled {
		t.Errorf("posts.enabled = %v", posts.Enabled)
	}
	if posts.Public == nil || !*posts.Public {
		t.Errorf("posts.public = %v", posts.Public)
	}
	if posts.Rows == nil || !*posts.Rows {
		t.Errorf("posts.rows = %v", posts.Rows)
	}
	if posts.MaxTTL == nil || *posts.MaxTTL != 60 {
		t.Errorf("posts.maxTtl = %v", posts.MaxTTL)
	}

	// The two that would survive a careless round trip looking plausible and meaning the opposite.
	drafts := m.Cache["drafts"]
	if drafts.Enabled == nil || *drafts.Enabled {
		t.Errorf("drafts.enabled = %v, want an explicit false", drafts.Enabled)
	}
	if drafts.Rows != nil {
		t.Errorf("drafts.rows = %v, want silence", drafts.Rows)
	}
	if ttl := m.Cache["zero"].MaxTTL; ttl == nil || *ttl != 0 {
		t.Errorf("zero.maxTtl = %v, want a zero that survived", ttl)
	}
}
