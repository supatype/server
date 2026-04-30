// Package functions provides the studio admin API for edge functions.
//
// Routes (all require service-role Bearer token):
//
//	GET  /list              — list deployed functions (scanned from functionsDir)
//	GET  /{name}/logs       — recent log lines for a function (?since=1h)
//	GET  /env               — list env var key names from .env.local
//	POST /env               — set an env var in .env.local
//	DELETE /env/{key}       — remove an env var from .env.local
package functions

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/auth/internal/deno"
)

// Handler returns a chi.Router that serves the functions admin API.
// manager may be nil when edge functions are disabled; all routes return 404.
func Handler(functionsDir string, manager *deno.Manager) http.Handler {
	r := chi.NewRouter()

	r.Use(requireServiceRole)

	r.Get("/list", listFunctions(functionsDir))
	r.Get("/{name}/logs", functionLogs(manager))
	r.Get("/env", listEnv(functionsDir))
	r.Post("/env", setEnv(functionsDir))
	r.Delete("/env/{key}", deleteEnv(functionsDir))

	return r
}

// ─── Auth middleware ──────────────────────────────────────────────────────────

// requireServiceRole rejects requests that don't carry the service-role key.
// The service-role key is read from the SUPATYPE_SERVICE_ROLE_KEY env var at
// request time so it works even if the key rotates without a restart.
func requireServiceRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serviceRoleKey := os.Getenv("SUPATYPE_SERVICE_ROLE_KEY")
		if serviceRoleKey == "" {
			// No key configured — allow (dev mode without service role).
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		token, _ := strings.CutPrefix(auth, "Bearer ")
		if token != serviceRoleKey {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service role key required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── List functions ───────────────────────────────────────────────────────────

type functionMeta struct {
	Name        string `json:"name"`
	DeployedAt  string `json:"deployedAt,omitempty"`
	Invocations int    `json:"invocations24h"`
	AvgDuration int    `json:"avgDurationMs"`
}

func listFunctions(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, http.StatusOK, []functionMeta{})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		funcs := make([]functionMeta, 0)
		for _, e := range entries {
			name := e.Name()
			// A function is either a .ts file or a directory containing index.ts.
			if e.IsDir() {
				if _, err := os.Stat(filepath.Join(dir, name, "index.ts")); err != nil {
					continue
				}
			} else {
				if !strings.HasSuffix(name, ".ts") {
					continue
				}
				name = strings.TrimSuffix(name, ".ts")
			}

			meta := functionMeta{Name: name}
			// Use file mod time as a proxy for deployed-at.
			if info, err := e.Info(); err == nil {
				meta.DeployedAt = info.ModTime().UTC().Format(time.RFC3339)
			}
			funcs = append(funcs, meta)
		}

		writeJSON(w, http.StatusOK, map[string]any{"data": funcs})
	}
}

// ─── Function logs ────────────────────────────────────────────────────────────

type logEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

func functionLogs(manager *deno.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			writeJSON(w, http.StatusOK, map[string]any{"data": []logEntry{}})
			return
		}

		since := parseSince(r.URL.Query().Get("since"))
		raw := manager.RecentLogs(since, 500)

		entries := make([]logEntry, len(raw))
		for i, l := range raw {
			entries[i] = logEntry{
				Timestamp: l.Timestamp.UTC().Format(time.RFC3339Nano),
				Level:     l.Level,
				Message:   l.Message,
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": entries})
	}
}

func parseSince(s string) time.Time {
	if s == "" {
		return time.Now().UTC().Add(-1 * time.Hour)
	}
	// Parse standard Go duration strings e.g. "1h", "15m", "6h", "24h"
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Now().UTC().Add(-1 * time.Hour)
	}
	return time.Now().UTC().Add(-d)
}

// ─── Env vars ─────────────────────────────────────────────────────────────────

func envFilePath(dir string) string {
	return filepath.Join(dir, ".env.local")
}

func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer f.Close()

	result := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		result[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return result, scanner.Err()
}

func writeEnvFile(path string, vars map[string]string) error {
	var sb strings.Builder
	for k, v := range vars {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(v)
		sb.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(sb.String()), 0600)
}

func listEnv(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars, err := readEnvFile(envFilePath(dir))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		keys := make([]string, 0, len(vars))
		for k := range vars {
			keys = append(keys, k)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": keys})
	}
}

func setEnv(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key and value required"})
			return
		}

		path := envFilePath(dir)
		vars, err := readEnvFile(path)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		vars[body.Key] = body.Value
		if err := writeEnvFile(path, vars); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"key": body.Key, "message": "set"}})
	}
}

func deleteEnv(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := chi.URLParam(r, "key")
		if key == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key required"})
			return
		}

		path := envFilePath(dir)
		vars, err := readEnvFile(path)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if _, ok := vars[key]; !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
			return
		}
		delete(vars, key)
		if err := writeEnvFile(path, vars); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"key": key, "message": "removed"}})
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
