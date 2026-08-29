// Package admin provides HTTP handlers for the /admin/v1 API.
//
// All routes require a service-role Bearer JWT verified against
// SUPATYPE_JWT_SECRET. In SUPATYPE_MODE=dev, JWT verification is skipped.
package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/valkey"
	"github.com/supatype/server/internal/modes"
	"github.com/supatype/server/internal/restcache"
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
// Mount it with r.Mount("/admin/v1", Handler(store, cfg, cache)).
func Handler(store apiconfig.Store, cfg *config.Config, vc valkey.Client) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/config/rest", restConfigRoute(store, cfg, vc))
	mux.HandleFunc("/config/graphql", graphQLConfigRoute(store))

	mux.HandleFunc("/database/credentials/status", only(http.MethodGet, credentialStatusHandler(cfg, vc)))
	mux.HandleFunc("/database/credentials/first-view", only(http.MethodPost, credentialFirstViewHandler(cfg, vc)))
	mux.HandleFunc("/database/credentials/rotate", only(http.MethodPost, credentialRotateHandler(cfg, vc)))

	mountCacheRoutes(mux, cfg, vc)

	return RequireServiceRole(cfg, mux)
}

// ─── REST configuration ───────────────────────────────────────────────────────

func restConfigRoute(store apiconfig.Store, cfg *config.Config, vc valkey.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			api, ok := loadAPIConfig(w, r, store)
			if !ok {
				return
			}
			utilities.WriteJSON(w, http.StatusOK, api.Rest)
		case http.MethodPatch:
			patchRestConfig(w, r, store, cfg, vc)
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

func patchRestConfig(w http.ResponseWriter, r *http.Request, store apiconfig.Store, cfg *config.Config, vc valkey.Client) {
	var body restPatch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// The cache is a paid feature on Cloud, so the two fields that configure it
	// are refused before anything is read or written. Checked against the
	// server's configuration, not the stored API config.
	if (body.CacheMaxTTL != nil || body.CacheTables != nil) &&
		!restcache.ServerCacheOffered(r.Context(), cfg, vc, r) {
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

	if saveAPIConfig(w, r, store, api) {
		utilities.WriteJSON(w, http.StatusOK, api.Rest)
	}
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
