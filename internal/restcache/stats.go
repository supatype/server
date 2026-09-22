package restcache

import (
	"sort"
	"sync"
	"time"
)

// Outcome is what the cache did with one request that asked to be cached.
//
// Only requests that asked are counted. A GET without `X-Supatype-Cache` was
// never a candidate, and counting it would bury the number this exists to
// produce — how much of what a project asked to cache actually was.
type Outcome string

const (
	OutcomeHit    Outcome = "hit"
	OutcomeMiss   Outcome = "miss"
	OutcomeBypass Outcome = "bypass"
)

// The reasons a request that asked for caching did not get it. Closed set: each
// one is a different thing to tell a user, and an open string would turn the
// response into whatever the call site last felt like writing.
const (
	// ReasonTier is the free tier. It is the reason the counter exists: it is
	// the only one where the answer is "upgrade", and it is counted per table
	// so a project can be shown what its own traffic would have saved.
	ReasonTier = "tier"
	// ReasonTableNotCached is a table with no cache rule, or a TTL of zero.
	ReasonTableNotCached = "table_not_cached"
	// ReasonNotDeclared is a table whose schema declares no cache, or declares
	// `enabled: false`. Apart from ReasonTableNotCached because the two are
	// different things to do about it: one is a box to tick in Studio, the
	// other is a line to add to the model and push.
	ReasonNotDeclared = "not_declared"
	// ReasonUnavailable is no keyspace configured at all.
	ReasonUnavailable = "unavailable"
	// ReasonConfigUnreadable is the API configuration failing to read, which
	// makes the cache rules unknown and is a bypass rather than a guess.
	ReasonConfigUnreadable = "config_unreadable"
	// ReasonCacheUnhealthy is a keyspace that answered with an error, or an
	// entry that would not decode.
	ReasonCacheUnhealthy = "cache_unhealthy"
	// ReasonStoreFailed is a response served from upstream that could not be
	// written back.
	ReasonStoreFailed = "store_failed"
)

// The caps that keep the counter's memory bounded.
//
// A table name is parsed out of the request path, which the caller controls, so
// an unbounded map here is a way to grow a pod's heap with a loop of requests
// for /rest/v1/{random}. The tenant is HMAC-verified on a managed pod but
// arrives in a header, so it gets a cap too. Traffic past either cap is folded
// into the overflow bucket rather than dropped: the totals stay true, and a
// tenant whose own tables stopped being named is told so.
const (
	maxTrackedTenants = 256
	maxTrackedTables  = 128
	overflowBucket    = "(other)"
)

// Counter counts what the response cache did, by tenant and table.
//
// pg_keyspace counts hits and misses per keyspace, worker and tenant, never per
// table: the key is opaque to it. This service parses the table out of the
// request to build that key, so it is the only place the per-table number can
// come from, and both screens that want it — the free tier's "here is what you
// are missing" and a paid project's per-table effectiveness — want the same
// counter.
//
// The zero value is not usable; NewCounter sets the start of the window. A nil
// *Counter is, deliberately: a deployment that does not want the counter passes
// nil and every method is a no-op, rather than each call site guarding.
type Counter struct {
	mu    sync.Mutex
	since time.Time

	// byTenant is tenant → table → counts. Both levels are capped; see the
	// constants above.
	byTenant map[string]map[string]*counts
}

// counts is one table's tally. Bypasses are kept per reason as well as in
// total, because "your tier" and "your keyspace is down" are the same number to
// a rate and entirely different things to say to someone.
type counts struct {
	hits     uint64
	misses   uint64
	bypasses uint64
	byReason map[string]uint64
}

// NewCounter returns a counter whose window starts now.
func NewCounter() *Counter {
	return &Counter{since: time.Now().UTC(), byTenant: map[string]map[string]*counts{}}
}

// Record adds one outcome. reason is ignored unless the outcome is a bypass.
//
// Cheap on purpose: one mutex, no allocation on the common path. It runs on
// every cacheable REST request, so anything more would be paid for by the
// requests it is measuring.
func (c *Counter) Record(tenant, table string, outcome Outcome, reason string) {
	if c == nil {
		return
	}
	if table == "" {
		// An RPC or a path with no table in it. There is nothing to attribute,
		// and attributing it to "" would invent a row in every report.
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	tables, ok := c.byTenant[tenant]
	if !ok {
		if len(c.byTenant) >= maxTrackedTenants {
			tenant = overflowBucket
			tables, ok = c.byTenant[tenant]
		}
		if !ok {
			tables = map[string]*counts{}
			c.byTenant[tenant] = tables
		}
	}

	entry, ok := tables[table]
	if !ok {
		if len(tables) >= maxTrackedTables {
			table = overflowBucket
			entry, ok = tables[table]
		}
		if !ok {
			entry = &counts{}
			tables[table] = entry
		}
	}

	switch outcome {
	case OutcomeHit:
		entry.hits++
	case OutcomeMiss:
		entry.misses++
	case OutcomeBypass:
		entry.bypasses++
		if reason != "" {
			if entry.byReason == nil {
				entry.byReason = map[string]uint64{}
			}
			entry.byReason[reason]++
		}
	}
}

// TableStats is one table's tally as the admin API reports it.
type TableStats struct {
	Table         string            `json:"table"`
	Hits          uint64            `json:"hits"`
	Misses        uint64            `json:"misses"`
	Bypasses      uint64            `json:"bypasses"`
	BypassReasons map[string]uint64 `json:"bypass_reasons,omitempty"`
}

// Report is one tenant's counters since the process started counting.
//
// Since is in the report because the numbers are cumulative and a total with no
// window is not a fact anyone can act on. A caller wanting a rate takes two
// reports and subtracts; a caller wanting to render one says "since <Since>".
// The window resets when the process restarts, which is the honest cost of
// counting in memory and is why Since is not optional.
type Report struct {
	Tenant string       `json:"tenant"`
	Since  time.Time    `json:"since"`
	Tables []TableStats `json:"tables"`
	Totals TableStats   `json:"totals"`

	// Limited says some traffic was counted under the overflow bucket rather
	// than under its own name, so the per-table rows do not account for
	// everything this tenant did. Without it a capped report looks complete.
	Limited bool `json:"limited"`
}

// Snapshot returns one tenant's counters, tables sorted by name so a client
// diffing two reports sees changes rather than map order.
func (c *Counter) Snapshot(tenant string) Report {
	if c == nil {
		return Report{Tenant: tenant, Tables: []TableStats{}, Totals: TableStats{Table: "*"}}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	report := Report{Tenant: tenant, Since: c.since, Tables: []TableStats{}, Totals: TableStats{Table: "*"}}
	for table, entry := range c.byTenant[tenant] {
		stats := TableStats{
			Table:    table,
			Hits:     entry.hits,
			Misses:   entry.misses,
			Bypasses: entry.bypasses,
		}
		if len(entry.byReason) > 0 {
			stats.BypassReasons = make(map[string]uint64, len(entry.byReason))
			for reason, n := range entry.byReason {
				stats.BypassReasons[reason] = n
			}
		}
		if table == overflowBucket {
			report.Limited = true
		}
		report.Totals.Hits += stats.Hits
		report.Totals.Misses += stats.Misses
		report.Totals.Bypasses += stats.Bypasses
		report.Tables = append(report.Tables, stats)
	}
	sort.Slice(report.Tables, func(i, j int) bool { return report.Tables[i].Table < report.Tables[j].Table })
	return report
}
