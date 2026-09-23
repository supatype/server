package cacheceiling

import (
	"reflect"
	"testing"

	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/proxy"
)

func b(v bool) *bool { return &v }
func i(v int) *int   { return &v }

func rest(ttl int, tables map[string]apiconfig.RestTableCacheConfig) apiconfig.RestConfig {
	return apiconfig.RestConfig{Schema: "public", MaxRows: 1000, CacheMaxTTL: ttl, CacheTables: tables}
}

func on(public bool) apiconfig.RestTableCacheConfig {
	return apiconfig.RestTableCacheConfig{Enabled: true, AllowPublic: public}
}

// ─── the property the whole package exists for ───────────────────────────────

func TestNarrowingNeverWidens(t *testing.T) {
	// Asserted as a property over every combination rather than case by case, because "may only
	// narrow" is the guarantee and a single missed branch is a silent widening. If this ever fails,
	// the schema has stopped being an honest description of what the system may do.
	for _, declared := range []map[string]proxy.TableCache{
		nil,
		{},
		{"posts": {}},
		{"posts": {Enabled: b(false)}},
		{"posts": {Enabled: b(true)}},
		{"posts": {Enabled: b(true), Public: b(false)}},
		{"posts": {Enabled: b(true), Public: b(true)}},
		{"posts": {Enabled: b(true), MaxTTL: i(30)}},
	} {
		for _, want := range []apiconfig.RestTableCacheConfig{
			{}, {Enabled: true}, {Enabled: true, AllowPublic: true}, {AllowPublic: true},
		} {
			got, _ := Narrow(declared, rest(600, map[string]apiconfig.RestTableCacheConfig{"posts": want}))
			out := got.CacheTables["posts"]

			if out.Enabled && !want.Enabled {
				t.Errorf("declared=%+v want=%+v: enabled was turned ON", declared["posts"], want)
			}
			if out.AllowPublic && !want.AllowPublic {
				t.Errorf("declared=%+v want=%+v: public was turned ON", declared["posts"], want)
			}
			if got.CacheMaxTTL > 600 {
				t.Errorf("declared=%+v: ttl was raised to %d", declared["posts"], got.CacheMaxTTL)
			}
		}
	}
}

// ─── a table the schema never declared ───────────────────────────────────────

func TestATableWithNoDeclarationCannotBeCachedByAnyone(t *testing.T) {
	got, adj := Narrow(map[string]proxy.TableCache{}, rest(60, map[string]apiconfig.RestTableCacheConfig{
		"posts": on(false),
	}))

	if got.CacheTables["posts"].Enabled {
		t.Error("a table with no declaration was cached")
	}
	if len(adj) != 1 || adj[0].Table != "posts" {
		t.Fatalf("adjustments = %+v, want one naming posts", adj)
	}
}

func TestAnAbsentDeclarationMapPermitsNothing(t *testing.T) {
	// An older push, or a schema with no cache blocks at all. Reading a missing declaration as
	// permission would turn an old manifest into an open door.
	got, _ := Narrow(nil, rest(60, map[string]apiconfig.RestTableCacheConfig{"posts": on(true)}))
	if got.CacheTables["posts"].Enabled || got.CacheTables["posts"].AllowPublic {
		t.Fatalf("posts = %+v, want nothing permitted", got.CacheTables["posts"])
	}
}

func TestTheEntryIsKeptSoTheOperatorSeesWhatHappened(t *testing.T) {
	// Dropping it would leave the admin API's response silent about the thing they just asked for,
	// and a box that unticks itself with no explanation gets ticked again.
	got, _ := Narrow(nil, rest(0, map[string]apiconfig.RestTableCacheConfig{"posts": on(false)}))
	if _, present := got.CacheTables["posts"]; !present {
		t.Fatal("the refused table vanished from the response")
	}
}

func TestTurningSomethingOffNeedsNoPermission(t *testing.T) {
	// Narrowing is always allowed, including for a table the schema never declared. Reporting an
	// adjustment here would be noise: nothing was refused.
	_, adj := Narrow(nil, rest(0, map[string]apiconfig.RestTableCacheConfig{"posts": {}}))
	if len(adj) != 0 {
		t.Fatalf("adjustments = %+v, want none for an off request", adj)
	}
}

// ─── the hard opt-out ────────────────────────────────────────────────────────

func TestEnabledFalseInTheSchemaSurvivesARuntimeEdit(t *testing.T) {
	declared := map[string]proxy.TableCache{"posts": {Enabled: b(false)}}
	got, adj := Narrow(declared, rest(60, map[string]apiconfig.RestTableCacheConfig{"posts": on(true)}))

	if got.CacheTables["posts"].Enabled {
		t.Error("a hard opt-out was overridden at runtime")
	}
	if got.CacheTables["posts"].AllowPublic {
		t.Error("public survived on a table that is not cached at all")
	}
	if len(adj) == 0 {
		t.Error("the refusal was silent")
	}
}

func TestSilenceIsNotAnOptOut(t *testing.T) {
	// A declaration that says nothing about `enabled` leaves the decision to the runtime — that is
	// the difference the pointer fields exist to preserve.
	declared := map[string]proxy.TableCache{"posts": {}}
	got, _ := Narrow(declared, rest(60, map[string]apiconfig.RestTableCacheConfig{"posts": on(false)}))
	if !got.CacheTables["posts"].Enabled {
		t.Fatal("a declaration that said nothing was read as an opt-out")
	}
}

// ─── public ──────────────────────────────────────────────────────────────────

func TestAPublicKeyNeedsTheSchemasPermission(t *testing.T) {
	// push refuses `public: true` on a table whose read rule varies by caller. That refusal is
	// worth nothing if an operator can tick the box afterwards.
	for name, declared := range map[string]proxy.TableCache{
		"silent":  {Enabled: b(true)},
		"refused": {Enabled: b(true), Public: b(false)},
	} {
		t.Run(name, func(t *testing.T) {
			got, adj := Narrow(map[string]proxy.TableCache{"posts": declared},
				rest(60, map[string]apiconfig.RestTableCacheConfig{"posts": on(true)}))

			if got.CacheTables["posts"].AllowPublic {
				t.Error("a shared cache key was allowed without the schema permitting it")
			}
			if !got.CacheTables["posts"].Enabled {
				t.Error("per-user caching was refused too, which narrows more than needed")
			}
			if len(adj) == 0 {
				t.Error("the refusal was silent")
			}
		})
	}
}

func TestAPublicKeyIsAllowedWhereTheSchemaPermitsIt(t *testing.T) {
	// The permitting side matters as much: a ceiling that refuses everything is not a ceiling.
	declared := map[string]proxy.TableCache{"posts": {Enabled: b(true), Public: b(true)}}
	got, adj := Narrow(declared, rest(60, map[string]apiconfig.RestTableCacheConfig{"posts": on(true)}))

	if !got.CacheTables["posts"].AllowPublic {
		t.Fatal("a permitted public key was refused")
	}
	if len(adj) != 0 {
		t.Fatalf("adjustments = %+v, want none", adj)
	}
}

// ─── the TTL cap ─────────────────────────────────────────────────────────────

func TestTheTTLIsCappedAtTheHighestDeclaredCeiling(t *testing.T) {
	declared := map[string]proxy.TableCache{
		"posts":    {Enabled: b(true), MaxTTL: i(60)},
		"comments": {Enabled: b(true), MaxTTL: i(30)},
	}
	got, adj := Narrow(declared, rest(3600, map[string]apiconfig.RestTableCacheConfig{"posts": on(false)}))

	if got.CacheMaxTTL != 60 {
		t.Fatalf("cache_max_ttl = %d, want the highest declared cap", got.CacheMaxTTL)
	}
	if len(adj) == 0 {
		t.Error("the cap was applied silently")
	}
}

func TestTheCapIsTheHighestNotTheLowest(t *testing.T) {
	// The lowest would let one model with a 5-second cap drag every other table down to five
	// seconds — narrowing nobody asked for, on tables whose own ceiling is far higher.
	declared := map[string]proxy.TableCache{
		"ticker": {Enabled: b(true), MaxTTL: i(5)},
		"posts":  {Enabled: b(true), MaxTTL: i(600)},
	}
	got, _ := Narrow(declared, rest(300, map[string]apiconfig.RestTableCacheConfig{"posts": on(false)}))
	if got.CacheMaxTTL != 300 {
		t.Fatalf("cache_max_ttl = %d, want the request left alone", got.CacheMaxTTL)
	}
}

func TestALowerRequestIsLeftAlone(t *testing.T) {
	declared := map[string]proxy.TableCache{"posts": {Enabled: b(true), MaxTTL: i(600)}}
	got, adj := Narrow(declared, rest(10, map[string]apiconfig.RestTableCacheConfig{"posts": on(false)}))
	if got.CacheMaxTTL != 10 {
		t.Fatalf("cache_max_ttl = %d, want 10", got.CacheMaxTTL)
	}
	if len(adj) != 0 {
		t.Fatalf("adjustments = %+v, want none", adj)
	}
}

func TestNoDeclaredTTLLeavesTheRequestAlone(t *testing.T) {
	// The schema declaring no cap is not a cap of zero.
	declared := map[string]proxy.TableCache{"posts": {Enabled: b(true)}}
	got, _ := Narrow(declared, rest(3600, map[string]apiconfig.RestTableCacheConfig{"posts": on(false)}))
	if got.CacheMaxTTL != 3600 {
		t.Fatalf("cache_max_ttl = %d, want untouched", got.CacheMaxTTL)
	}
}

func TestAZeroTTLRequestIsNotCapped(t *testing.T) {
	declared := map[string]proxy.TableCache{"posts": {Enabled: b(true), MaxTTL: i(60)}}
	got, adj := Narrow(declared, rest(0, map[string]apiconfig.RestTableCacheConfig{"posts": on(false)}))
	if got.CacheMaxTTL != 0 || len(adj) != 0 {
		t.Fatalf("ttl = %d adj = %+v, want zero left alone", got.CacheMaxTTL, adj)
	}
}

// ─── shape ───────────────────────────────────────────────────────────────────

func TestAPatchWithNoTablesIsUntouched(t *testing.T) {
	in := rest(60, nil)
	got, adj := Narrow(map[string]proxy.TableCache{"posts": {Enabled: b(true)}}, in)
	if !reflect.DeepEqual(got, in) || adj != nil {
		t.Fatalf("got %+v / %+v, want the input unchanged", got, adj)
	}
}

func TestTheRestOfTheConfigIsCarriedThrough(t *testing.T) {
	got, _ := Narrow(map[string]proxy.TableCache{"posts": {Enabled: b(true)}},
		rest(60, map[string]apiconfig.RestTableCacheConfig{"posts": on(false)}))
	if got.Schema != "public" || got.MaxRows != 1000 {
		t.Fatalf("config = %+v, want schema and max_rows intact", got)
	}
}

func TestAdjustmentsAreDeterministic(t *testing.T) {
	// Two identical requests must produce identical responses, or a caller diffing them sees
	// changes that did not happen.
	declared := map[string]proxy.TableCache{}
	in := rest(0, map[string]apiconfig.RestTableCacheConfig{"b": on(false), "a": on(false), "c": on(false)})
	_, first := Narrow(declared, in)
	_, second := Narrow(declared, in)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("%+v != %+v", first, second)
	}
	if len(first) != 3 || first[0].Table != "a" || first[2].Table != "c" {
		t.Fatalf("adjustments = %+v, want sorted", first)
	}
}

// ─── Mode B: declaration only ────────────────────────────────────────────────

func TestTheRowCacheFollowsTheSchemaAndNothingElse(t *testing.T) {
	declared := map[string]proxy.TableCache{
		"posts":    {Enabled: b(true), Rows: b(true)},
		"comments": {Enabled: b(true), Rows: b(false)},
		"tags":     {Enabled: b(true)},
		"events":   {Rows: b(true)},
	}
	got := RowCacheTables(declared)

	// `events` has rows without enabled: the two caches are separate, and a table may take the row
	// cache without the response cache.
	if !reflect.DeepEqual(got, []string{"events", "posts"}) {
		t.Fatalf("row cache tables = %v, want [events posts]", got)
	}
}

func TestNoDeclarationMeansNoRowCache(t *testing.T) {
	if got := RowCacheTables(nil); len(got) != 0 {
		t.Fatalf("row cache tables = %v, want none", got)
	}
	if got := RowCacheTables(map[string]proxy.TableCache{"posts": {Enabled: b(true)}}); len(got) != 0 {
		t.Fatalf("row cache tables = %v, want none without rows:true", got)
	}
}

// ─── the ceiling on the read path ────────────────────────────────────────────

// Permits answers the question the read path asks, which is not quite the one Narrow asks: not
// "what may be stored", but "may this request be served from a cache at all, and how long for".
func TestWhatTheSchemaPermitsPerTable(t *testing.T) {
	for name, tc := range map[string]struct {
		declared map[string]proxy.TableCache
		want     Permitted
		wantOK   bool
	}{
		"no declaration at all": {
			declared: nil,
		},
		"another table declared": {
			declared: map[string]proxy.TableCache{"comments": {Enabled: b(true)}},
		},
		"declared off": {
			declared: map[string]proxy.TableCache{"posts": {Enabled: b(false)}},
		},
		// A declaration that names only `rows` or only `maxTtl` is still a declaration: `enabled`
		// absent is permission, and only an explicit false is the opt-out. Narrow reads it the
		// same way, and the two paths disagreeing about one manifest is the bug this pins.
		"declared with no enabled key": {
			declared: map[string]proxy.TableCache{"posts": {Rows: b(true)}},
			wantOK:   true,
		},
		"declared on": {
			declared: map[string]proxy.TableCache{"posts": {Enabled: b(true)}},
			wantOK:   true,
		},
		"declared public": {
			declared: map[string]proxy.TableCache{"posts": {Enabled: b(true), Public: b(true)}},
			want:     Permitted{Public: true},
			wantOK:   true,
		},
		"declared with a cap": {
			declared: map[string]proxy.TableCache{"posts": {Enabled: b(true), MaxTTL: i(30)}},
			want:     Permitted{MaxTTL: 30},
			wantOK:   true,
		},
		// Zero is not a cap of zero seconds, which would be a declaration that permits caching and
		// then forbids every entry. It is the absence of a declared cap.
		"declared with a zero cap": {
			declared: map[string]proxy.TableCache{"posts": {Enabled: b(true), MaxTTL: i(0)}},
			wantOK:   true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := Permits(tc.declared, "posts")
			if ok != tc.wantOK {
				t.Fatalf("permitted = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("permits = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The per-table cap is the half narrowTTL leaves to the read path, where the table is known.
func TestTheLowerOfTheTwoCapsWins(t *testing.T) {
	for name, tc := range map[string]struct {
		projectWide int
		declared    int
		want        int
	}{
		"no declared cap":             {projectWide: 60, want: 60},
		"declared cap is tighter":     {projectWide: 60, declared: 30, want: 30},
		"project-wide cap is tighter": {projectWide: 10, declared: 30, want: 10},
		"the caps agree":              {projectWide: 30, declared: 30, want: 30},
		// The off switch is not a cap to be improved on: a declared 30 here would mean this
		// function raising a TTL, which is the one thing the package forbids.
		"caching off project-wide":      {projectWide: 0, declared: 30, want: 0},
		"nothing declared, caching off": {projectWide: 0, want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			if got := CapTTL(tc.projectWide, Permitted{MaxTTL: tc.declared}); got != tc.want {
				t.Fatalf("CapTTL(%d, %d) = %d, want %d", tc.projectWide, tc.declared, got, tc.want)
			}
		})
	}
}
