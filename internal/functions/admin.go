// Package functions provides the studio admin API for edge functions.
//
// Routes (all require service-role Bearer token):
//
//	GET  /list              — list deployed functions (scanned from functionsDir)
//	GET  /{name}/logs       — recent log lines for a function (?since=1h)
//	GET  /env               — list shared env var key names from .env.local
//	POST /env               — set a shared env var in .env.local
//	DELETE /env/{key}       — remove a shared env var from .env.local
//	GET  /{name}/env        — list function-specific env var key names
//	POST /{name}/env        — set a function-specific env var
//	DELETE /{name}/env/{key}— remove a function-specific env var
package functions

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/deno"
	"github.com/supatype/server/internal/modes"
	"github.com/supatype/server/internal/utilities"
)

// LogSource is the part of the edge-function worker this API reads.
//
// Narrowed to the one method so the log endpoint can be exercised without
// supervising a real process, and so this package does not depend on the
// supervisor to serve a list of log lines.
type LogSource interface {
	RecentLogs(since time.Time, n int) []deno.LogLine
}

// Handler returns a chi.Router that serves the functions admin API.
// manager may be nil when edge functions are disabled; the log route is then
// empty rather than absent.
func Handler(cfg *config.Config, functionsDir string, manager LogSource) http.Handler {
	r := chi.NewRouter()

	r.Use(RequireServiceRoleMiddleware(cfg))

	r.Get("/list", listFunctions(functionsDir))
	r.Get("/{name}/logs", functionLogs(manager))
	shared, perFunction := sharedEnvFile(functionsDir), functionEnvFile(functionsDir)
	r.Get("/env", listEnv(shared))
	r.Post("/env", setEnv(shared))
	r.Delete("/env/{key}", deleteEnv(shared))
	r.Get("/{name}/env", listEnv(perFunction))
	r.Post("/{name}/env", setEnv(perFunction))
	r.Delete("/{name}/env/{key}", deleteEnv(perFunction))

	return r
}

// ─── Auth middleware ──────────────────────────────────────────────────────────

// RequireServiceRoleMiddleware rejects requests that don't carry the service-role key.
func RequireServiceRoleMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return requireServiceRole(cfg, next) }
}

// requireServiceRole rejects requests that don't carry the service-role key.
//
// The key comes from configuration rather than being read per request. That
// read was justified as supporting rotation without a restart, but nothing
// rotates it in place: the deployment sets it in the environment and a change
// replaces the pod. Reading it once keeps the whole surface in one place.
func requireServiceRole(cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(cfg.Mode) == "dev" {
			next.ServeHTTP(w, r)
			return
		}

		if strings.TrimSpace(cfg.ServiceRoleKey) == "" {
			utilities.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "service role key not configured"})
			return
		}
		if !modes.ServiceRoleBearer(r, cfg.ServiceRoleKey) {
			utilities.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "service role key required"})
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
				utilities.WriteJSON(w, http.StatusOK, []functionMeta{})
				return
			}
			utilities.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
				if strings.HasPrefix(name, ".") {
					continue
				}
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

		utilities.WriteJSON(w, http.StatusOK, map[string]any{"data": funcs})
	}
}

// ─── Function logs ────────────────────────────────────────────────────────────

type logEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

func functionLogs(manager LogSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			utilities.WriteJSON(w, http.StatusOK, map[string]any{"data": []logEntry{}})
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
		utilities.WriteJSON(w, http.StatusOK, map[string]any{"data": entries})
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

// ─── Env files ────────────────────────────────────────────────────────────────

// validFunctionName is what a function may be called.
//
// The name becomes part of a filename, so it is checked rather than cleaned. A
// chi path parameter cannot contain a separator, which is what makes the
// current routes safe, but that is a property of the router and not of this
// code, and a future route with a wildcard would silently remove it.
var validFunctionName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// envFile resolves which env file a request is about.
//
// The three operations — list, set, remove — are the same whether the file is
// the shared one or one function's; only which file differs. They were written
// out twice each, six handlers for three behaviours, and the copies had already
// drifted over which errors they checked.
type envFile func(*http.Request) (string, error)

// sharedEnvFile is .env.local, applying to every function.
func sharedEnvFile(dir string) envFile {
	return func(*http.Request) (string, error) {
		return filepath.Join(dir, ".env.local"), nil
	}
}

// functionEnvFile is .env.{name}.local, applying to one.
func functionEnvFile(dir string) envFile {
	return func(r *http.Request) (string, error) {
		name := strings.TrimSpace(chi.URLParam(r, "name"))
		if name == "" {
			return "", errors.New("function name required")
		}
		if !validFunctionName.MatchString(name) {
			return "", errors.New("function name must be letters, digits, hyphens or underscores")
		}
		return filepath.Join(dir, ".env."+name+".local"), nil
	}
}

// envRoot opens the directory an env file lives in, so both the read and the
// write are scoped to it. The file name carries a function name that arrived on
// a request, and the validation the route applies to it is the first line of
// defence rather than the only one.
func envRoot(path string) (*os.Root, string, error) {
	dir, name := filepath.Split(filepath.Clean(path))
	if dir == "" {
		dir = "."
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", err
	}
	return root, name, nil
}

func readEnvFile(path string) (map[string]string, error) {
	root, name, err := envRoot(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer func() {
		_ = root.Close()
	}()

	f, err := root.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()

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

// writeEnvFile rewrites the file in key order, so setting one variable does not
// reshuffle the rest and turn every change into a whole-file diff.
func writeEnvFile(path string, vars map[string]string) error {
	var sb strings.Builder
	for _, k := range sortedKeys(vars) {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(vars[k])
		sb.WriteByte('\n')
	}

	root, name, err := envRoot(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = root.Close()
	}()

	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(sb.String())
	return errors.Join(writeErr, f.Close())
}

func sortedKeys(vars map[string]string) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// load resolves the file for this request and reads it, answering the caller
// itself when either fails.
func load(w http.ResponseWriter, r *http.Request, file envFile) (path string, vars map[string]string, ok bool) {
	path, err := file(r)
	if err != nil {
		utilities.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return "", nil, false
	}
	vars, err = readEnvFile(path)
	if err != nil {
		utilities.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return "", nil, false
	}
	return path, vars, true
}

// listEnv answers with the key names. Values are never returned: the point of
// putting a secret in an env file is that reading it back is not an API.
func listEnv(file envFile) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, vars, ok := load(w, r, file)
		if !ok {
			return
		}
		utilities.WriteJSON(w, http.StatusOK, map[string]any{"data": sortedKeys(vars)})
	}
}

// save writes the file back and answers, so the two mutating endpoints report
// a failed write the same way rather than in two places that can drift.
func save(w http.ResponseWriter, path string, vars map[string]string, key, message string) {
	if err := writeEnvFile(path, vars); err != nil {
		utilities.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	utilities.WriteJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"key": key, "message": message}})
}

func setEnv(file envFile) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
			utilities.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "key and value required"})
			return
		}

		path, vars, ok := load(w, r, file)
		if !ok {
			return
		}
		vars[body.Key] = body.Value
		save(w, path, vars, body.Key, "set")
	}
}

func deleteEnv(file envFile) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := chi.URLParam(r, "key")
		if key == "" {
			utilities.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "key required"})
			return
		}

		path, vars, ok := load(w, r, file)
		if !ok {
			return
		}
		if _, present := vars[key]; !present {
			utilities.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
			return
		}
		delete(vars, key)
		save(w, path, vars, key, "removed")
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────
