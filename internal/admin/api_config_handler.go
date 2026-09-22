// Package admin provides HTTP handlers for the /admin/v1 API.
//
// All routes require a service-role Bearer JWT verified against
// SUPATYPE_JWT_SECRET. In SUPATYPE_MODE=dev, JWT verification is skipped.
package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/cacheceiling"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/keyspace"
	"github.com/supatype/server/internal/modes"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/restcache"
	"github.com/supatype/server/internal/rowcache"
	"github.com/supatype/server/internal/utilities"
)

var validSchema = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_$]{0,62}$`)

// writeErr is the error shape every route in this package answers with.
func writeErr(w http.ResponseWriter, status int, message string) {
	utilities.WriteJSON(w, status, map[string]string{"error": message})
}

// only answers 405 for any method but this one.
//
// Three credential routes each opened with their own copy of the check, which
// is three chances to name the wrong method.
func only(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		next(w, r)
	}
}

// inRange validates a bound the API documents, and says what the bound is.
func inRange(name string, value, low, high int) error {
	if value < low || value > high {
		return fmt.Errorf("%s must be %d–%d", name, low, high)
	}
	return nil
}

// Handler returns a mux covering all /admin/v1 routes.
// Mount it with r.Mount("/admin/v1", Handler(store, cfg, cache, platform, stats)).
//
// Two keyspaces, because the routes below want different ones. The cache routes
// read and delete cached responses, which live in the project's own keyspace.
// The credential routes read and write the KEK-wrapped managed password, whose
// only copy is platform state — served from the project keyspace they would
// report "no credentials" for a project that has them, and a rotation would
// write the new secret somewhere the control plane will never look. Eligibility
// is platform state too: it comes from the tenant configuration.
//
// On a single-keyspace deployment the caller passes the same client twice, which
// is exactly what it was before the split.
//
// Stats may be nil, which serves the cache-statistics route with an empty
// report rather than removing it: a screen that asks for numbers should be told
// there are none, not given a 404 to interpret. Monitor may be nil on the same
// principle, and for the commoner reason — a deployment with no database DSN.
//
// A struct rather than a parameter list because Cache and Platform have the
// same type and opposite meanings. Swapping them at a call site compiles
// cleanly and turns the response cache off for every paid project, since a miss
// for tenant configuration in the project keyspace is indistinguishable from a
// tenant nobody ever published. That is the failure the two-keyspace split was
// mostly about, and positional arguments are how it would come back.
type Deps struct {
	Store  apiconfig.Store
	Config *config.Config
	// Cache is the project's keyspace: where cached responses are stored.
	Cache keyspace.Client
	// Platform is the platform keyspace: tenant configuration, eligibility, and
	// the KEK-wrapped managed credential. On a single-keyspace deployment this
	// is the same client as Cache, which is exactly what it was before the split.
	Platform keyspace.Client
	Stats    *restcache.Counter
	// Monitor reads pg_keyspace's own views out of the project's Postgres. They
	// are SQL over shared memory, so neither keyspace client can answer for them.
	Monitor StatsQuerier
	// Declared is what the schema permits caching, per table, for this request's tenant. It comes
	// from the route manifest, which is where `supatype push` puts it.
	//
	// A function rather than a map because a multi-tenant pod resolves its manifest per request: a
	// map captured at mount time would hand every tenant the first one's ceiling.
	//
	// Nil, or a nil return, means no declaration — which is "nothing may be cached", not "no
	// constraint". See cacheceiling.Narrow.
	Declared func(*http.Request) map[string]proxy.TableCache
}

func Handler(d Deps) http.Handler {
	store, cfg, vc, platform, stats := d.Store, d.Config, d.Cache, d.Platform, d.Stats
	mux := http.NewServeMux()

	mux.HandleFunc("/config/rest", restConfigRoute(store, cfg, platform, d.Monitor, d.Declared))
	mux.HandleFunc("/config/graphql", graphQLConfigRoute(store))

	mux.HandleFunc("/database/credentials/status", only(http.MethodGet, credentialStatusHandler(cfg, platform)))
	mux.HandleFunc("/database/credentials/first-view", only(http.MethodPost, credentialFirstViewHandler(cfg, platform)))
	mux.HandleFunc("/database/credentials/rotate", only(http.MethodPost, credentialRotateHandler(cfg, platform)))

	mountCacheRoutes(mux, cfg, vc, platform, stats)
	mountKeyspaceStatsRoutes(mux, cfg, d.Monitor)

	return RequireServiceRole(cfg, mux)
}

// ─── REST configuration ───────────────────────────────────────────────────────

func restConfigRoute(store apiconfig.Store, cfg *config.Config, tenants keyspace.Client, db rowcache.DB, declared declaredFor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			api, ok := loadAPIConfig(w, r, store)
			if !ok {
				return
			}
			utilities.WriteJSON(w, http.StatusOK, api.Rest)
		case http.MethodPatch:
			patchRestConfig(w, r, store, cfg, tenants, db, declared)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

// restPatch is what a caller may change about the REST proxy.
type restPatch struct {
	Schema      *string                                    `json:"schema"`
	MaxRows     *int                                       `json:"max_rows"`
	CacheMaxTTL *int                                       `json:"cache_max_ttl"`
	CacheTables *map[string]apiconfig.RestTableCacheConfig `json:"cache_tables"`
}

func patchRestConfig(w http.ResponseWriter, r *http.Request, store apiconfig.Store, cfg *config.Config, tenants keyspace.Client, db rowcache.DB, declared declaredFor) {
	var body restPatch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// The cache is a paid feature on Cloud, so the two fields that configure it
	// are refused before anything is read or written. Checked against the
	// server's configuration, not the stored API config.
	if (body.CacheMaxTTL != nil || body.CacheTables != nil) &&
		!restcache.ServerCacheOffered(r.Context(), cfg, tenants, r) {
		utilities.WriteJSON(w, http.StatusForbidden, map[string]string{
			"error":   "rest_cache_not_available",
			"message": "Server-side REST caching is included on paid Cloud plans and self-host.",
		})
		return
	}

	api, ok := loadAPIConfig(w, r, store)
	if !ok {
		return
	}

	if body.Schema != nil {
		if !validSchema.MatchString(*body.Schema) {
			writeErr(w, http.StatusBadRequest, "invalid schema name")
			return
		}
		api.Rest.Schema = *body.Schema
	}
	if body.MaxRows != nil {
		if err := inRange("max_rows", *body.MaxRows, 1, 100_000); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		api.Rest.MaxRows = *body.MaxRows
	}
	if body.CacheMaxTTL != nil {
		if err := inRange("cache_max_ttl", *body.CacheMaxTTL, 0, 86_400); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		api.Rest.CacheMaxTTL = *body.CacheMaxTTL
	}
	if body.CacheTables != nil {
		api.Rest.CacheTables = *body.CacheTables
	}

	// The ceiling, before the save rather than after: what is stored has to be what is permitted,
	// or a later read would serve the unnarrowed request as though it had been accepted. §13.2.
	ceiling := declaredTables(declared, r)
	narrowed, adjustments := cacheceiling.Narrow(ceiling, api.Rest)
	api.Rest = narrowed

	if !saveAPIConfig(w, r, store, api) {
		return
	}

	// After the save, not before: the allowlist is the source of truth and the
	// registrations follow it. Reconciling first would leave a database
	// registered for tables a failed write never allowed.
	//
	// Only when the allowlist itself moved. A schema or max_rows change is not
	// a reason to touch the row cache, and a reconcile on every PATCH would put
	// database round trips behind edits that have nothing to do with caching.
	resp := restConfigResponse{RestConfig: api.Rest, Adjusted: adjustments}
	if body.CacheTables != nil {
		resp.RowCache = reconcileRowCache(r, db, api.Rest.Schema, ceiling)
	}
	utilities.WriteJSON(w, http.StatusOK, resp)
}

// restConfigResponse is the REST config, plus what changing the allowlist did
// to the row cache.
//
// Embedded so the shape callers already parse is unchanged: RestCacheBrowser
// reads cache_max_ttl and cache_tables off the top level and must keep working
// against a server that grew a field.
type restConfigResponse struct {
	apiconfig.RestConfig
	RowCache *rowCacheReconcile `json:"row_cache,omitempty"`
	// Adjusted is everything the schema's ceiling refused or reduced.
	//
	// Reported rather than applied in silence: an operator who ticks a box and watches it untick
	// itself has been told nothing, and will tick it again.
	Adjusted []cacheceiling.Adjustment `json:"adjusted,omitempty"`
}

// declaredFor resolves this request's cache ceiling. See Deps.Declared.
type declaredFor func(*http.Request) map[string]proxy.TableCache

func declaredTables(f declaredFor, r *http.Request) map[string]proxy.TableCache {
	if f == nil {
		return nil
	}
	return f(r)
}

type rowCacheReconcile struct {
	rowcache.Outcome
	// Set when the allowlist was saved but the row cache could not be reached.
	// The response cache is unaffected, and saying so is the difference between
	// a warning and an error nobody can act on.
	Unavailable bool   `json:"unavailable,omitempty"`
	Error       string `json:"error,omitempty"`
}

// reconcileRowCache brings registrations in line with the saved allowlist, and
// never fails the request for it.
//
// The two caches ride one switch (§12.2) but they are not one cache. The
// allowlist is saved and the response cache is already live by the time this
// runs; a 502 here would tell a user their change did not take when it did.
// So every outcome is reported in the body and the status stays 200.
func reconcileRowCache(r *http.Request, db rowcache.DB, schema string, declared map[string]proxy.TableCache) *rowCacheReconcile {
	// **The schema, not the runtime allowlist.** The first cut of registration read
	// `cache_tables[].enabled`, which made Mode B a runtime toggle. It is not one: the row cache
	// is eventual with a bound rather than read-your-writes, and whether a table tolerates a read
	// returning the previous row is a design-time invariant its author knows and an operator
	// flipping a switch at 3am does not. Mode A gets a runtime switch; Mode B does not (§13.3).
	tables := cacheceiling.RowCacheTables(declared)

	if schema == "" {
		schema = apiconfig.DefaultApiConfig().Rest.Schema
	}

	outcome, err := rowcache.Reconcile(r.Context(), db, schema, tables)
	switch {
	case errors.Is(err, rowcache.ErrUnavailable):
		return &rowCacheReconcile{Unavailable: true}
	case err != nil:
		return &rowCacheReconcile{Error: err.Error()}
	case outcome.Changed():
		return &rowCacheReconcile{Outcome: outcome}
	}
	return nil
}

// ─── GraphQL configuration ────────────────────────────────────────────────────

func graphQLConfigRoute(store apiconfig.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			api, ok := loadAPIConfig(w, r, store)
			if !ok {
				return
			}
			utilities.WriteJSON(w, http.StatusOK, api.GraphQL)
		case http.MethodPatch:
			patchGraphQLConfig(w, r, store)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

// graphQLPatch is what a caller may change about the GraphQL proxy.
type graphQLPatch struct {
	Introspection *bool `json:"introspection"`
	MaxQueryDepth *int  `json:"max_query_depth"`
	MaxRows       *int  `json:"max_rows"`
}

func patchGraphQLConfig(w http.ResponseWriter, r *http.Request, store apiconfig.Store) {
	var body graphQLPatch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	api, ok := loadAPIConfig(w, r, store)
	if !ok {
		return
	}

	if body.Introspection != nil {
		api.GraphQL.Introspection = *body.Introspection
	}
	if body.MaxQueryDepth != nil {
		if err := inRange("max_query_depth", *body.MaxQueryDepth, 1, 50); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		api.GraphQL.MaxQueryDepth = *body.MaxQueryDepth
	}
	if body.MaxRows != nil {
		if err := inRange("max_rows", *body.MaxRows, 1, 100_000); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		api.GraphQL.MaxRows = *body.MaxRows
	}

	if saveAPIConfig(w, r, store, api) {
		utilities.WriteJSON(w, http.StatusOK, api.GraphQL)
	}
}

// ─── The store ────────────────────────────────────────────────────────────────

// loadAPIConfig reads the stored configuration, answering the caller itself
// when it cannot.
//
// Named apart from the server's own *config.Config, which used to be shadowed
// by a variable of the same name inside these handlers: two different things
// called cfg, one of them deciding whether a paid feature is available.
func loadAPIConfig(w http.ResponseWriter, r *http.Request, store apiconfig.Store) (apiconfig.ApiConfig, bool) {
	api, err := store.Get(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return apiconfig.ApiConfig{}, false
	}
	return api, true
}

func saveAPIConfig(w http.ResponseWriter, r *http.Request, store apiconfig.Store, api apiconfig.ApiConfig) bool {
	if err := store.Set(r.Context(), api); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	return true
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

// RequireServiceRole wraps next with service-role enforcement. In dev mode all
// requests pass through without a token.
//
// This answers 403 where the functions admin API answers 401 for the same
// condition. The difference is preserved rather than tidied away: a client may
// already distinguish them, and changing a status code is a behaviour change
// that does not belong in a configuration refactor.
func RequireServiceRole(cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(cfg.Mode) == "dev" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.TrimSpace(cfg.ServiceRoleKey) == "" {
			writeErr(w, http.StatusForbidden, "service role key not configured")
			return
		}
		if !modes.ServiceRoleBearer(r, cfg.ServiceRoleKey) {
			writeErr(w, http.StatusForbidden, "service role key required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
