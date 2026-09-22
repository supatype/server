// Package cacheceiling applies the rule that decides who wins when the schema and an operator
// disagree about caching.
//
//	The model declares what is PERMITTED. Studio and the admin API decide what is ACTIVE,
//	and may only narrow it.
//
// (plan §13.2)
//
// So a table with no declaration cannot be cached by anyone, `maxTtl: 60` is a cap Studio can lower
// to 10 and not raise to 300, and `enabled: false` is a hard opt-out that survives every runtime
// edit. Both properties that matter are kept: the schema stays an honest description of what the
// system may do, and an operator keeps a lever they can pull during an incident without a schema
// push. Drift is bounded and always in the safe direction.
//
// The alternatives, and why not. *Declared wins outright* removes the incident lever. *Runtime wins
// and push does not clobber* means the schema stops describing reality, invisibly. *Push resets to
// declared* silently reverts someone's mitigation at the worst possible moment.
package cacheceiling

import (
	"fmt"
	"sort"

	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/proxy"
)

// Adjustment is one thing the ceiling refused or reduced, in the terms a user would read.
//
// Returned rather than logged because the admin API answers with them: an operator who ticks a box
// and sees it silently untick has been told nothing, and will try again.
type Adjustment struct {
	Table  string `json:"table"`
	Reason string `json:"reason"`
}

// Narrow clamps a requested REST configuration to what the schema permits.
//
// It never widens. Every branch here either leaves the request alone or makes it more restrictive,
// which is what makes "the runtime may only narrow" a property of the code rather than a promise in
// a comment.
//
// A nil `declared` means the manifest carries no cache declaration at all — an older push, or a
// schema with no `cache` blocks. That is "nothing is permitted", and it is the safe reading: the
// alternative is to treat a missing declaration as permission, which turns an old manifest into an
// open door.
func Narrow(
	declared map[string]proxy.TableCache,
	requested apiconfig.RestConfig,
) (apiconfig.RestConfig, []Adjustment) {
	var adjustments []Adjustment
	if len(requested.CacheTables) == 0 {
		return requested, nil
	}

	out := requested
	out.CacheTables = make(map[string]apiconfig.RestTableCacheConfig, len(requested.CacheTables))

	tables := make([]string, 0, len(requested.CacheTables))
	for table := range requested.CacheTables {
		tables = append(tables, table)
	}
	// Deterministic, so two identical requests produce identical responses and a test does not
	// have to sort what it reads back.
	sort.Strings(tables)

	for _, table := range tables {
		want := requested.CacheTables[table]
		ceiling, isDeclared := declared[table]

		if !isDeclared {
			// Not "off by default" — not permitted at all. Keeping the entry with Enabled false
			// rather than dropping it means the admin API's response shows the operator what
			// happened to the thing they asked for.
			if want.Enabled || want.AllowPublic {
				adjustments = append(adjustments, Adjustment{
					Table:  table,
					Reason: "the schema does not declare a cache for this table, so nothing may cache it",
				})
			}
			out.CacheTables[table] = apiconfig.RestTableCacheConfig{}
			continue
		}

		got := want

		// enabled:false in the schema is an opt-out, not a default. It survives every runtime edit.
		if want.Enabled && ceiling.Enabled != nil && !*ceiling.Enabled {
			got.Enabled = false
			got.AllowPublic = false
			adjustments = append(adjustments, Adjustment{
				Table:  table,
				Reason: "the schema sets cache.enabled to false for this table, which a runtime edit cannot override",
			})
		}

		// A shared cache key is permitted by the schema or not at all. push refuses it on a table
		// whose read rule varies by caller, and that refusal is worth nothing if the runtime can
		// simply tick the box afterwards.
		if got.AllowPublic && (ceiling.Public == nil || !*ceiling.Public) {
			got.AllowPublic = false
			adjustments = append(adjustments, Adjustment{
				Table:  table,
				Reason: "the schema does not permit a public cache key for this table; entries stay per-user",
			})
		}

		out.CacheTables[table] = got
	}

	if ttl, adj := narrowTTL(declared, requested); adj != nil {
		out.CacheMaxTTL = ttl
		adjustments = append(adjustments, *adj)
	}

	return out, adjustments
}

// narrowTTL caps the project-wide TTL at the most permissive per-table ceiling.
//
// Project-wide against per-table is a genuine mismatch: `cache_max_ttl` is one number for the whole
// project and `maxTtl` is declared per model. Capping at the *largest* declared value is the only
// choice that does not silently shorten unrelated tables — the smallest would let one model with a
// 5-second cap drag every other table down to five seconds, which is narrowing nobody asked for.
//
// Per-table enforcement of a tighter ceiling belongs on the read path, where the table being served
// is known. This is the blunt half, and it is here so a request for 3600 against a schema whose
// highest declared cap is 60 is answered rather than accepted and quietly ignored.
func narrowTTL(declared map[string]proxy.TableCache, requested apiconfig.RestConfig) (int, *Adjustment) {
	if requested.CacheMaxTTL <= 0 {
		return 0, nil
	}
	highest, any := 0, false
	for _, c := range declared {
		if c.MaxTTL != nil && *c.MaxTTL > highest {
			highest, any = *c.MaxTTL, true
		}
	}
	if !any || requested.CacheMaxTTL <= highest {
		return requested.CacheMaxTTL, nil
	}
	return highest, &Adjustment{
		Table: "*",
		Reason: fmt.Sprintf(
			"the schema's highest declared cache.maxTtl is %ds, so cache_max_ttl was capped there instead of %ds",
			highest, requested.CacheMaxTTL),
	}
}

// RowCacheTables returns the tables the schema declares for the Mode B row cache.
//
// **The declaration is the only source.** Unlike everything else here there is no runtime switch to
// narrow, because the row cache is eventual with a bound rather than read-your-writes: whether a
// table tolerates a read returning the previous row for up to the staleness window is a design-time
// invariant its author knows and an operator flipping a toggle at 3am does not (§13.3).
//
// This replaces reading the runtime allowlist, which is what the first cut of registration did.
func RowCacheTables(declared map[string]proxy.TableCache) []string {
	out := make([]string, 0, len(declared))
	for table, c := range declared {
		if c.Rows != nil && *c.Rows {
			out = append(out, table)
		}
	}
	sort.Strings(out)
	return out
}
