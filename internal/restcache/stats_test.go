package restcache

import (
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace"
	"github.com/supatype/server/internal/data/keyspace/keyspacetest"
)

// ─── What the middleware counts ──────────────────────────────────────────────

// The counter exists to answer two product questions from one number: what a
// free project's traffic would have been served from cache, and how each of a
// paid project's tables is doing. Both are wrong unless every way a request can
// end is attributed, so every ending is exercised here through the middleware
// rather than by calling Record directly.
func TestEveryOutcomeIsCounted(t *testing.T) {
	disabled := false

	failingRead := keyspacetest.New()
	failingRead.GetErr = keyspacetest.ErrFailed

	failingWrite := keyspacetest.New()
	failingWrite.SetErr = keyspacetest.ErrFailed

	freeTier := keyspacetest.New().WithTenant("proj-1", &keyspace.TenantConfig{RestCacheEnabled: &disabled})
	managed := func(d Deps) Deps {
		d.Config = &config.Config{Mode: "managed", ManagedProjectRef: "proj-1", JWTSecret: "secret"}
		return d
	}

	for name, tc := range map[string]struct {
		deps    func(*Counter) Deps
		request *http.Request
		// warm serves one request first, so the one being measured is a hit.
		warm       bool
		wantTenant string
		want       TableStats
	}{
		"a miss": {
			deps:       func(c *Counter) Deps { return withStats(deps(keyspacetest.New(), cachingStore()), c) },
			request:    request("/posts", "max-age=30"),
			wantTenant: "local",
			want:       TableStats{Table: cachedTable, Misses: 1},
		},
		"a hit": {
			deps:       func(c *Counter) Deps { return withStats(deps(keyspacetest.New(), cachingStore()), c) },
			request:    request("/posts", "max-age=30"),
			warm:       true,
			wantTenant: "local",
			want:       TableStats{Table: cachedTable, Hits: 1, Misses: 1},
		},
		"the free tier": {
			deps:       func(c *Counter) Deps { return managed(withStats(deps(freeTier, cachingStore()), c)) },
			request:    request("/posts", "max-age=30"),
			wantTenant: "proj-1",
			want: TableStats{
				Table: cachedTable, Bypasses: 1,
				BypassReasons: map[string]uint64{ReasonTier: 1},
			},
		},
		"a tenant whose config was never published": {
			deps:       func(c *Counter) Deps { return managed(withStats(deps(keyspacetest.New(), cachingStore()), c)) },
			request:    request("/posts", "max-age=30"),
			wantTenant: "proj-1",
			want: TableStats{
				Table: cachedTable, Bypasses: 1,
				BypassReasons: map[string]uint64{ReasonConfigUnreadable: 1},
			},
		},
		"a keyspace that will not answer the tenant lookup": {
			deps:       func(c *Counter) Deps { return managed(withStats(deps(failingTenantLookup(), cachingStore()), c)) },
			request:    request("/posts", "max-age=30"),
			wantTenant: "proj-1",
			want: TableStats{
				Table: cachedTable, Bypasses: 1,
				BypassReasons: map[string]uint64{ReasonCacheUnhealthy: 1},
			},
		},
		"no keyspace at all": {
			deps:       func(c *Counter) Deps { return withStats(deps(nil, cachingStore()), c) },
			request:    request("/posts", "max-age=30"),
			wantTenant: "local",
			want: TableStats{
				Table: cachedTable, Bypasses: 1,
				BypassReasons: map[string]uint64{ReasonUnavailable: 1},
			},
		},
		// Two different tables to add caching to, and two different things to do about it: the
		// first needs a line in the model, the second a box ticked in Studio.
		"a table the schema does not declare": {
			deps:       func(c *Counter) Deps { return withStats(deps(keyspacetest.New(), cachingStore()), c) },
			request:    request("/comments", "max-age=30"),
			wantTenant: "local",
			want: TableStats{
				Table: "comments", Bypasses: 1,
				BypassReasons: map[string]uint64{ReasonNotDeclared: 1},
			},
		},
		"a declared table with no cache rule": {
			deps: func(c *Counter) Deps {
				return withStats(deps(keyspacetest.New(), staticStore{cfg: apiconfig.DefaultApiConfig()}), c)
			},
			request:    request("/posts", "max-age=30"),
			wantTenant: "local",
			want: TableStats{
				Table: cachedTable, Bypasses: 1,
				BypassReasons: map[string]uint64{ReasonTableNotCached: 1},
			},
		},
		"a config that cannot be read": {
			deps: func(c *Counter) Deps {
				return withStats(deps(keyspacetest.New(), staticStore{err: keyspacetest.ErrFailed}), c)
			},
			request:    request("/posts", "max-age=30"),
			wantTenant: "local",
			want: TableStats{
				Table: cachedTable, Bypasses: 1,
				BypassReasons: map[string]uint64{ReasonConfigUnreadable: 1},
			},
		},
		"a cache that will not be read": {
			deps:       func(c *Counter) Deps { return withStats(deps(failingRead, cachingStore()), c) },
			request:    request("/posts", "max-age=30"),
			wantTenant: "local",
			want: TableStats{
				Table: cachedTable, Bypasses: 1,
				BypassReasons: map[string]uint64{ReasonCacheUnhealthy: 1},
			},
		},
		"a cache that will not be written": {
			deps:       func(c *Counter) Deps { return withStats(deps(failingWrite, cachingStore()), c) },
			request:    request("/posts", "max-age=30"),
			wantTenant: "local",
			want: TableStats{
				Table: cachedTable, Bypasses: 1,
				BypassReasons: map[string]uint64{ReasonStoreFailed: 1},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			counter := NewCounter()
			d := tc.deps(counter)
			if tc.warm {
				serve(d, &upstream{}, request(tc.request.URL.Path, "max-age=30"))
			}
			serve(d, &upstream{}, tc.request)

			report := counter.Snapshot(tc.wantTenant)
			if len(report.Tables) != 1 {
				t.Fatalf("report has %d tables, want 1: %+v", len(report.Tables), report.Tables)
			}
			assertTableStats(t, report.Tables[0], tc.want)
		})
	}
}

// A request that never asked to be cached is not a missed opportunity, and
// counting it would bury the number that is.
func TestARequestThatDidNotAskIsNotCounted(t *testing.T) {
	counter := NewCounter()
	d := withStats(deps(nil, cachingStore()), counter)

	serve(d, &upstream{}, request("/posts", ""))

	if report := counter.Snapshot("local"); len(report.Tables) != 0 {
		t.Errorf("a request that did not ask for caching was counted: %+v", report.Tables)
	}
}

// A write is not a cache outcome at all: it never reaches the decision.
func TestAWriteIsNotCounted(t *testing.T) {
	counter := NewCounter()
	d := withStats(deps(keyspacetest.New(), cachingStore()), counter)

	req := request("/posts", "max-age=30")
	req.Method = http.MethodPost
	serve(d, &upstream{}, req)

	if report := counter.Snapshot("local"); len(report.Tables) != 0 {
		t.Errorf("a write was counted as a cache outcome: %+v", report.Tables)
	}
}

// An RPC has no table to attribute to. Filing it under "" would put a row named
// nothing in every report.
func TestAPathWithNoTableIsNotCounted(t *testing.T) {
	counter := NewCounter()
	counter.Record("local", "", OutcomeMiss, "")

	if report := counter.Snapshot("local"); len(report.Tables) != 0 {
		t.Errorf("a request with no table was counted: %+v", report.Tables)
	}
}

// ─── The counter itself ──────────────────────────────────────────────────────

// A deployment that passes no counter must not have to guard every call site,
// and a report from one is empty rather than absent.
func TestANilCounterIsANoOp(t *testing.T) {
	var counter *Counter
	counter.Record("local", "posts", OutcomeHit, "")

	report := counter.Snapshot("local")
	if len(report.Tables) != 0 || report.Totals.Hits != 0 {
		t.Errorf("a nil counter reported something: %+v", report)
	}
	if !report.Since.IsZero() {
		t.Error("a nil counter should not claim a window it never had")
	}
}

// The totals are the sum of the rows, and the rows are sorted so a client that
// diffs two reports sees what changed rather than what moved.
func TestAReportTotalsAndSorts(t *testing.T) {
	counter := NewCounter()
	for _, table := range []string{"posts", "comments", "authors"} {
		counter.Record("proj-1", table, OutcomeHit, "")
		counter.Record("proj-1", table, OutcomeMiss, "")
		counter.Record("proj-1", table, OutcomeBypass, ReasonTier)
	}
	// Another tenant's traffic must not appear in this one's report.
	counter.Record("proj-2", "posts", OutcomeHit, "")

	report := counter.Snapshot("proj-1")
	if got := []string{report.Tables[0].Table, report.Tables[1].Table, report.Tables[2].Table}; got[0] != "authors" || got[1] != "comments" || got[2] != "posts" {
		t.Errorf("tables are not sorted: %v", got)
	}
	assertTableStats(t, report.Totals, TableStats{Table: "*", Hits: 3, Misses: 3, Bypasses: 3})
	if report.Limited {
		t.Error("nothing was capped, so the report should not say it was limited")
	}
	if report.Since.IsZero() || report.Since.After(time.Now().UTC()) {
		t.Errorf("Since = %v, want the moment counting started", report.Since)
	}
}

// A tenant nobody has counted for gets an empty report, not a missing one: the
// screen asking has a project and no traffic, which is a state to render.
func TestAnUnknownTenantReportsNothing(t *testing.T) {
	report := NewCounter().Snapshot("proj-unknown")
	if len(report.Tables) != 0 || report.Totals.Hits != 0 {
		t.Errorf("got %+v", report)
	}
	if report.Tenant != "proj-unknown" {
		t.Errorf("tenant = %q", report.Tenant)
	}
}

// The table comes out of a URL path the caller controls, so the map has to be
// bounded. Traffic past the cap is folded into one bucket rather than dropped,
// and the report says it stopped naming things.
func TestTablesPastTheCapFoldIntoOneBucketAndSaySo(t *testing.T) {
	counter := NewCounter()
	for i := range maxTrackedTables {
		counter.Record("proj-1", "table-"+strconv.Itoa(i), OutcomeHit, "")
	}
	for i := range 3 {
		counter.Record("proj-1", "overflow-"+strconv.Itoa(i), OutcomeHit, "")
	}

	report := counter.Snapshot("proj-1")
	if len(report.Tables) != maxTrackedTables+1 {
		t.Fatalf("report has %d rows, want the cap plus the overflow bucket", len(report.Tables))
	}
	if !report.Limited {
		t.Error("a capped report must say it is not accounting for everything")
	}
	if report.Totals.Hits != uint64(maxTrackedTables)+3 {
		t.Errorf("totals lost the overflowing traffic: %d", report.Totals.Hits)
	}
	// The named rows keep their own numbers; only the unnamed share a bucket.
	for _, row := range report.Tables {
		if row.Table == overflowBucket && row.Hits != 3 {
			t.Errorf("overflow bucket = %d hits, want 3", row.Hits)
		}
	}
}

// The tenant arrives in a header, so it is capped the same way.
func TestTenantsPastTheCapFoldIntoOneBucket(t *testing.T) {
	counter := NewCounter()
	for i := range maxTrackedTenants {
		counter.Record("proj-"+strconv.Itoa(i), "posts", OutcomeHit, "")
	}
	counter.Record("proj-one-too-many", "posts", OutcomeHit, "")
	counter.Record("proj-one-more-still", "posts", OutcomeHit, "")

	if report := counter.Snapshot("proj-one-too-many"); len(report.Tables) != 0 {
		t.Error("a tenant past the cap should not get a report of its own")
	}
	if report := counter.Snapshot(overflowBucket); report.Totals.Hits != 2 {
		t.Errorf("the overflow tenant holds %d hits, want 2", report.Totals.Hits)
	}
	if report := counter.Snapshot("proj-0"); report.Totals.Hits != 1 {
		t.Errorf("a tenant inside the cap lost its own numbers: %+v", report)
	}
}

// The reasons map in a report is a copy. A caller that mutates what it was
// handed must not be editing the counter.
func TestAReportDoesNotShareItsReasonsWithTheCounter(t *testing.T) {
	counter := NewCounter()
	counter.Record("proj-1", "posts", OutcomeBypass, ReasonTier)

	report := counter.Snapshot("proj-1")
	report.Tables[0].BypassReasons[ReasonTier] = 99

	if again := counter.Snapshot("proj-1"); again.Tables[0].BypassReasons[ReasonTier] != 1 {
		t.Errorf("the counter was edited through its own report: %d", again.Tables[0].BypassReasons[ReasonTier])
	}
}

// It is written from every request handler at once and read from the admin API.
func TestTheCounterIsSafeUnderConcurrentUse(t *testing.T) {
	counter := NewCounter()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 50 {
				counter.Record("proj-"+strconv.Itoa(i%2), "posts", OutcomeHit, "")
			}
		}()
		go func() {
			defer wg.Done()
			for range 50 {
				counter.Snapshot("proj-0")
			}
		}()
	}
	wg.Wait()

	total := counter.Snapshot("proj-0").Totals.Hits + counter.Snapshot("proj-1").Totals.Hits
	if total != 8*50 {
		t.Errorf("counted %d, want %d", total, 8*50)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func withStats(d Deps, c *Counter) Deps {
	d.Stats = c
	return d
}

func cachingStore() staticStore { return staticStore{cfg: cachingConfig(false)} }

// failingTenantLookup is a keyspace that answers the tenant read with an error,
// which is a different thing from one that answers "no such tenant".
func failingTenantLookup() *keyspacetest.Client {
	c := keyspacetest.New()
	c.TenantErr = keyspacetest.ErrFailed
	return c
}

func assertTableStats(t *testing.T, got, want TableStats) {
	t.Helper()
	if got.Table != want.Table || got.Hits != want.Hits || got.Misses != want.Misses || got.Bypasses != want.Bypasses {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
	if len(got.BypassReasons) != len(want.BypassReasons) {
		t.Fatalf("bypass reasons = %v, want %v", got.BypassReasons, want.BypassReasons)
	}
	for reason, n := range want.BypassReasons {
		if got.BypassReasons[reason] != n {
			t.Errorf("bypass reason %q = %d, want %d", reason, got.BypassReasons[reason], n)
		}
	}
}
