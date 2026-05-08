// Package admin provides HTTP handlers for the /admin/v1 API.
//
// All routes require a service-role Bearer JWT verified against GOTRUE_JWT_SECRET.
// In SUPATYPE_MODE=dev, JWT verification is skipped.
package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/supatype/auth/internal/apiconfig"
	"github.com/supatype/auth/internal/serverconf"
	"github.com/supatype/auth/internal/valkey"
)

var validSchema = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_$]{0,62}$`)

// Handler returns a mux covering all /admin/v1 routes.
// Mount it with r.Mount("/admin/v1", Handler(store)).
func Handler(store apiconfig.Store, cfg *serverconf.ServerConfig, vc *valkey.Client) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/config/rest", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg, err := store.Get(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, cfg.Rest)

		case http.MethodPatch:
			var body struct {
				Schema  *string `json:"schema"`
				MaxRows *int    `json:"max_rows"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
				return
			}
			cfg, err := store.Get(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if body.Schema != nil {
				if !validSchema.MatchString(*body.Schema) {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid schema name"})
					return
				}
				cfg.Rest.Schema = *body.Schema
			}
			if body.MaxRows != nil {
				if *body.MaxRows < 1 || *body.MaxRows > 100_000 {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "max_rows must be 1–100000"})
					return
				}
				cfg.Rest.MaxRows = *body.MaxRows
			}
			if err := store.Set(r.Context(), cfg); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, cfg.Rest)

		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/config/graphql", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg, err := store.Get(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, cfg.GraphQL)

		case http.MethodPatch:
			var body struct {
				Introspection *bool `json:"introspection"`
				MaxQueryDepth *int  `json:"max_query_depth"`
				MaxRows       *int  `json:"max_rows"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
				return
			}
			cfg, err := store.Get(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if body.Introspection != nil {
				cfg.GraphQL.Introspection = *body.Introspection
			}
			if body.MaxQueryDepth != nil {
				if *body.MaxQueryDepth < 1 || *body.MaxQueryDepth > 50 {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "max_query_depth must be 1–50"})
					return
				}
				cfg.GraphQL.MaxQueryDepth = *body.MaxQueryDepth
			}
			if body.MaxRows != nil {
				if *body.MaxRows < 1 || *body.MaxRows > 100_000 {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "max_rows must be 1–100000"})
					return
				}
				cfg.GraphQL.MaxRows = *body.MaxRows
			}
			if err := store.Set(r.Context(), cfg); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, cfg.GraphQL)

		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})

	mux.HandleFunc("/database/credentials/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		credentialStatusHandler(cfg, vc).ServeHTTP(w, r)
	})
	mux.HandleFunc("/database/credentials/first-view", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		credentialFirstViewHandler(cfg, vc).ServeHTTP(w, r)
	})
	mux.HandleFunc("/database/credentials/rotate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		credentialRotateHandler(cfg, vc).ServeHTTP(w, r)
	})

	return RequireServiceRole(mux)
}

// RequireServiceRole wraps next with service-role JWT enforcement.
// In SUPATYPE_MODE=dev, all requests pass through without a token.
func RequireServiceRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(os.Getenv("SUPATYPE_MODE")) == "dev" {
			next.ServeHTTP(w, r)
			return
		}
		key := strings.TrimSpace(os.Getenv("SUPATYPE_SERVICE_ROLE_KEY"))
		if key == "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "service role key not configured"})
			return
		}
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		token, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "service role key required"})
			return
		}
		token = strings.TrimSpace(token)
		if token != key {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "service role key required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
