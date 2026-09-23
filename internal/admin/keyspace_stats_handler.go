package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/utilities"
)

// StatsQuerier is the read path to pg_keyspace's own monitoring views.
//
// They are SQL views over shared memory, living in the project's Postgres
// rather than in the keyspace, so they are not reachable over RESP and the
// keyspace client cannot answer for them. *pgxpool.Pool satisfies this.
//
// An interface rather than the pool, because every one of the states below —
// the extension absent, the feature off, no traffic yet — has to be rendered as
// itself, and the only honest way to know that they are is to be able to write
// a test that produces each one.
type StatsQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Every field the two panels in §12.2 need, in one round trip each.
//
// `hit_pct` is deliberately absent from both structs as a number: see hitPct.
const (
	keyspaceStatsSQL = `SELECT workers, partitions, entries, hits, misses, evictions,
		       sets, tombstones, rehashes, arena_used_bytes, arena_capacity_bytes, hit_pct
		  FROM supacache.pg_stat_keyspace`

	rowCacheStatsSQL = `SELECT entries, hits, misses, arena_used_bytes, arena_capacity_bytes,
		       hit_pct, coherent, decode_enabled, beat_age_ms, stale_after_ms,
		       registrations, registrations_loaded, datname, slot_lost,
		       participating_databases, incoherent_databases
		  FROM supacache.pg_stat_keyspace_rowcache`

	rowCacheDatabasesSQL = `SELECT datname, state, registrations, coherent, slot_lost,
		       beat_age_ms, stale_after_ms, worker_pid
		  FROM supacache.pg_stat_keyspace_rowcache_databases
		 ORDER BY datname`
)

// keyspaceStats is the Effectiveness and Capacity panels.
//
// Every counter carries a _total suffix because every counter is cumulative
// since the segment started and is not reset by reading. A raw number is
// meaningless to a reader who assumes otherwise, and a hit rate computed from
// the totals shows 94% on a cache that has been broken for an hour — the rate
// has to come from a delta between two reads, which is the caller's job and is
// the reason these are named the way they are.
type keyspaceStats struct {
	Workers    int   `json:"workers"`
	Partitions int   `json:"partitions"`
	Entries    int64 `json:"entries"`

	HitsTotal       int64 `json:"hits_total"`
	MissesTotal     int64 `json:"misses_total"`
	EvictionsTotal  int64 `json:"evictions_total"`
	SetsTotal       int64 `json:"sets_total"`
	TombstonesTotal int64 `json:"tombstones_total"`
	RehashesTotal   int64 `json:"rehashes_total"`

	ArenaUsedBytes     int64 `json:"arena_used_bytes"`
	ArenaCapacityBytes int64 `json:"arena_capacity_bytes"`

	// Null, never 0, before the first lookup. A cache nobody has read from has
	// no hit ratio, and rendering one as 0% invents an outage on a fresh
	// deployment. The extension is careful to return NULL here and this struct
	// is careful to keep it.
	HitPct *float64 `json:"hit_pct"`
}

// rowCacheStatus is the Row cache health panel.
//
// State is first because it is the question. The views are EMPTY when
// rowcache_decode is off — not zeroed — so a handler that unpacked rows into a
// struct and returned it would report a disabled feature as a perfectly healthy
// one with no traffic. That distinction is the panel's whole job.
type rowCacheStatus struct {
	// One of: unavailable, off, idle, participating, incoherent.
	//
	//   unavailable   — the extension is not installed, or this role cannot read
	//                   its views. Needs GRANT pg_monitor.
	//   off           — installed, rowcache_decode is off. Not an error and not
	//                   healthy: nothing is being cached or invalidated.
	//   idle          — on, but this database has no registered tables, so it
	//                   takes no slot and no turn. That is by design.
	//   participating — on, registered, and inside its staleness window.
	//   incoherent    — on and registered, but behind or its slot was lost.
	//                   Reads fail closed to the heap: answers stay correct and
	//                   nothing is being served from the cache.
	State string `json:"state"`

	// Present only when State is not unavailable.
	DecodeEnabled bool   `json:"decode_enabled"`
	Database      string `json:"database,omitempty"`

	// The user-facing promise, in milliseconds, read live rather than assumed:
	// Mode B is eventual with a bound, not read-your-writes. A primary-key read
	// may return the previous row for up to this long after another connection's
	// UPDATE. It grows with the number of participating databases once they
	// outnumber the invalidation pool, because a database waiting its turn is
	// behind by the cycle time by design — which is exactly why a UI must read
	// it instead of printing the 200ms default.
	StaleAfterMS int64 `json:"stale_after_ms"`

	Coherent bool `json:"coherent"`
	// True when the server cut this database's slot loose for retaining too much
	// WAL. An unknown set of invalidations was never delivered, so no heartbeat
	// can vouch for what is cached — which is why this is its own field and not
	// folded into Coherent.
	SlotLost  bool   `json:"slot_lost"`
	BeatAgeMS *int64 `json:"beat_age_ms"`

	Registrations int64 `json:"registrations"`
	// Whether this database's registrations have been read into the worker yet.
	//
	// `boolean` in `supacache.pg_stat_keyspace_rowcache`, and an `int64` here, so every scan of
	// that view failed with `cannot scan bool (OID 16) in binary format into *int64` and the
	// endpoint answered 502.
	//
	// Invisible until the row cache is switched on, which is what makes it worth a comment: the
	// view is EMPTY while `rowcache_decode` is off, so there is no row to scan, no error, and the
	// handler correctly reports `off`. Turning the feature on is what starts returning a row, so
	// the panel reported a working row cache as a missing one — and Studio renders a non-404/503
	// failure as "not running", which is the same words it uses for genuinely off.
	RegistrationsLoaded bool `json:"registrations_loaded"`

	Entries            int64    `json:"entries"`
	HitsTotal          int64    `json:"hits_total"`
	MissesTotal        int64    `json:"misses_total"`
	ArenaUsedBytes     int64    `json:"arena_used_bytes"`
	ArenaCapacityBytes int64    `json:"arena_capacity_bytes"`
	HitPct             *float64 `json:"hit_pct"`

	ParticipatingDatabases int64 `json:"participating_databases"`
	IncoherentDatabases    int64 `json:"incoherent_databases"`

	// Cluster-wide, and the reason the cache fails closed per database rather
	// than as a whole. Absent when State is unavailable or off.
	Databases []rowCacheDatabase `json:"databases,omitempty"`
}

type rowCacheDatabase struct {
	Database      string `json:"database"`
	State         string `json:"state"`
	Registrations int64  `json:"registrations"`
	Coherent      bool   `json:"coherent"`
	SlotLost      bool   `json:"slot_lost"`
	BeatAgeMS     *int64 `json:"beat_age_ms"`
	StaleAfterMS  int64  `json:"stale_after_ms"`
	WorkerPID     *int32 `json:"worker_pid"`
}

func mountKeyspaceStatsRoutes(mux *http.ServeMux, cfg *config.Config, monitor StatsQuerier) {
	mux.HandleFunc("/cache/keyspace", only(http.MethodGet, keyspaceStatsHandler(cfg, monitor)))
	mux.HandleFunc("/cache/rowcache", only(http.MethodGet, rowCacheHandler(cfg, monitor)))
}

func keyspaceStatsHandler(_ *config.Config, monitor StatsQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if monitor == nil {
			writeErr(w, http.StatusServiceUnavailable, "no database configured for keyspace statistics")
			return
		}
		rows, err := monitor.Query(r.Context(), keyspaceStatsSQL)
		if err != nil {
			writeStatsErr(w, err)
			return
		}
		defer rows.Close()

		var s keyspaceStats
		if !rows.Next() {
			// The view is one aggregate row and always returns it, so no row means
			// the extension is not loaded rather than an empty cache.
			writeErr(w, http.StatusServiceUnavailable, "pg_keyspace is not running on this database")
			return
		}
		if err := rows.Scan(&s.Workers, &s.Partitions, &s.Entries, &s.HitsTotal, &s.MissesTotal,
			&s.EvictionsTotal, &s.SetsTotal, &s.TombstonesTotal, &s.RehashesTotal,
			&s.ArenaUsedBytes, &s.ArenaCapacityBytes, &s.HitPct); err != nil {
			writeStatsErr(w, err)
			return
		}
		if err := rows.Err(); err != nil {
			writeStatsErr(w, err)
			return
		}
		utilities.WriteJSON(w, http.StatusOK, s)
	}
}

func rowCacheHandler(_ *config.Config, monitor StatsQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if monitor == nil {
			utilities.WriteJSON(w, http.StatusOK, rowCacheStatus{State: "unavailable"})
			return
		}
		status, err := readRowCache(r.Context(), monitor)
		if err != nil {
			writeStatsErr(w, err)
			return
		}
		utilities.WriteJSON(w, http.StatusOK, status)
	}
}

// readRowCache resolves the five states. Split out because the mapping from
// rows-or-no-rows to a state is the part worth testing, and it is not reachable
// through an http.Handler without a live Postgres.
func readRowCache(ctx context.Context, monitor StatsQuerier) (rowCacheStatus, error) {
	rows, err := monitor.Query(ctx, rowCacheStatsSQL)
	if err != nil {
		// Both of these are "the extension is not there", not an outage: a
		// deployment without pg_keyspace, or a role without pg_monitor, and the
		// panel renders the same thing for each.
		if isMissingRelation(err) {
			return rowCacheStatus{State: "unavailable"}, nil
		}
		return rowCacheStatus{}, err
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return rowCacheStatus{}, err
		}
		// Installed, decoding off. Not zeroes — no rows at all.
		return rowCacheStatus{State: "off"}, nil
	}

	var s rowCacheStatus
	if err := rows.Scan(&s.Entries, &s.HitsTotal, &s.MissesTotal, &s.ArenaUsedBytes,
		&s.ArenaCapacityBytes, &s.HitPct, &s.Coherent, &s.DecodeEnabled, &s.BeatAgeMS,
		&s.StaleAfterMS, &s.Registrations, &s.RegistrationsLoaded, &s.Database,
		&s.SlotLost, &s.ParticipatingDatabases, &s.IncoherentDatabases); err != nil {
		return rowCacheStatus{}, err
	}
	if err := rows.Err(); err != nil {
		return rowCacheStatus{}, err
	}
	rows.Close()

	switch {
	case !s.DecodeEnabled:
		// The view answers even with decoding off, and reports coherent=true —
		// correctly, because with no decoder the operator owns coherence and the
		// planner hook does not fail anything closed. Reporting that as a healthy
		// cache is the error rule 2 is about, so the state wins over the column.
		s.State = "off"
	case s.Registrations == 0:
		s.State = "idle"
	case !s.Coherent || s.SlotLost:
		s.State = "incoherent"
	default:
		s.State = "participating"
	}

	if s.State != "off" {
		dbs, err := readRowCacheDatabases(ctx, monitor)
		if err != nil {
			return rowCacheStatus{}, err
		}
		s.Databases = dbs
	}
	return s, nil
}

func readRowCacheDatabases(ctx context.Context, monitor StatsQuerier) ([]rowCacheDatabase, error) {
	rows, err := monitor.Query(ctx, rowCacheDatabasesSQL)
	if err != nil {
		if isMissingRelation(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	out := []rowCacheDatabase{}
	for rows.Next() {
		var d rowCacheDatabase
		if err := rows.Scan(&d.Database, &d.State, &d.Registrations, &d.Coherent,
			&d.SlotLost, &d.BeatAgeMS, &d.StaleAfterMS, &d.WorkerPID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// isMissingRelation reports whether err is "this database has no pg_keyspace",
// in either of the two ways that presents.
//
// 42P01 is the view not existing; 42501 is it existing and this role not being
// a pg_monitor member, which is the one every fresh provision hits. Both mean
// the panel has nothing to show and neither is worth a 502 — a deployment
// without the extension is a supported deployment.
func isMissingRelation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 42P01 undefined_table, 42501 insufficient_privilege, 3F000 invalid_schema_name.
		return pgErr.Code == "42P01" || pgErr.Code == "42501" || pgErr.Code == "3F000"
	}
	return false
}

func writeStatsErr(w http.ResponseWriter, err error) {
	writeErr(w, http.StatusBadGateway, err.Error())
}
