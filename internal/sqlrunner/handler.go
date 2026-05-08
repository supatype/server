// Package sqlrunner provides the studio SQL-runner HTTP handler.
//
// Route (requires service-role Bearer token unless explicitly insecure):
//
//	POST /sql   — execute a SQL query and return rows as JSON
//
// The Postgres search_path is determined server-side from the JWT role claim.
// Clients may request a schema override in the body, but it is only honoured
// when the JWT role is "service_role" (or SUPATYPE_MODE=dev).
package sqlrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/sirupsen/logrus"
)

const (
	queryTimeout = 30 * time.Second
	maxRows      = 10_000
	insecureEnv  = "SUPATYPE_SQLRUNNER_INSECURE"
)

// Handler returns an http.Handler that serves the SQL runner endpoint.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(os.Stderr, "[sqlrunner] %s %s\n", r.Method, r.URL.Path)

		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		if !checkServiceRole(r) {
			fmt.Fprintf(os.Stderr, "[sqlrunner] auth rejected\n")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service role key required"})
			return
		}

		var body struct {
			Query  string `json:"query"`
			Schema string `json:"schema,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Query) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query is required"})
			return
		}

		// Resolve schema server-side. The JWT role determines what is allowed;
		// the client-supplied schema is only used when the role is service_role.
		schema := resolveSchema(r.Header.Get("Authorization"), body.Schema)
		fmt.Fprintf(os.Stderr, "[sqlrunner] schema=%s\n", schema)

		pool, err := getPool(r.Context())
		if err != nil {
			fmt.Fprintf(os.Stderr, "[sqlrunner] pool error: %v\n", err)
			logrus.WithError(err).Error("sqlrunner: database not available")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database not available: " + err.Error()})
			return
		}
		fmt.Fprintf(os.Stderr, "[sqlrunner] pool ok, schema=%s executing query\n", schema)

		// Use a transaction so SET LOCAL is scoped to this request only and does
		// not leak the search_path to other connections in the pool.
		queryCtx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		defer cancel()

		tx, err := pool.Begin(queryCtx)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "failed to begin transaction: " + err.Error()})
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()

		// set_config with is_local=true is equivalent to SET LOCAL inside a txn.
		if _, err := tx.Exec(queryCtx, "SELECT pg_catalog.set_config('search_path', $1, true)", schema); err != nil {
			fmt.Fprintf(os.Stderr, "[sqlrunner] set search_path error: %v\n", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to set schema: " + err.Error()})
			return
		}

		rows, err := tx.Query(queryCtx, body.Query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[sqlrunner] query error: %v\n", err)
			logrus.WithError(err).Error("sqlrunner: query failed")
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()

		fieldDescs := rows.FieldDescriptions()
		colNames := make([]string, len(fieldDescs))
		for i, fd := range fieldDescs {
			colNames[i] = string(fd.Name)
		}

		result := make([]map[string]interface{}, 0)
		for rows.Next() {
			if len(result) >= maxRows {
				rows.Close()
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
					"error": fmt.Sprintf("result exceeds %d row limit", maxRows),
				})
				return
			}
			vals, err := rows.Values()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			row := make(map[string]interface{}, len(colNames))
			for i, col := range colNames {
				row[col] = vals[i]
			}
			result = append(result, row)
		}
		if err := rows.Err(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		if err := tx.Commit(queryCtx); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to commit: " + err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"rows":     result,
			"rowCount": len(result),
			"schema":   schema,
		})
	})
}

// ─── Schema resolution ────────────────────────────────────────────────────────

var validIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// resolveSchema determines the Postgres schema to use for a request.
//
//   - In dev mode any requested schema is accepted (no JWT needed).
//   - For service_role JWTs the client may request an override schema.
//   - All other roles are locked to SUPATYPE_DB_SCHEMA (default: "public").
func resolveSchema(authHeader, requestedSchema string) string {
	defaultSchema := os.Getenv("SUPATYPE_DB_SCHEMA")
	if defaultSchema == "" {
		defaultSchema = "public"
	}

	// Explicit insecure mode: trust the requested schema if valid, else use default.
	if sqlRunnerInsecure() {
		if requestedSchema != "" && validIdentifier.MatchString(requestedSchema) {
			return requestedSchema
		}
		return defaultSchema
	}

	// Parse JWT role claim from Authorization header (no sig verify needed —
	// the service-role key check in checkServiceRole already authenticated the
	// request; we're only reading the role for schema routing).
	role := jwtRole(authHeader)

	if role == "service_role" && requestedSchema != "" && validIdentifier.MatchString(requestedSchema) {
		return requestedSchema
	}

	return defaultSchema
}

// jwtRole extracts the "role" claim from a Bearer JWT without verifying the
// signature. Signature verification is handled by checkServiceRole; here we
// only need the plaintext claim for schema routing.
func jwtRole(authHeader string) string {
	token, _ := strings.CutPrefix(authHeader, "Bearer ")
	if token == "" {
		return "anon"
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "anon"
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "anon"
	}
	var claims struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Role == "" {
		return "anon"
	}
	return claims.Role
}

// ─── Service role auth ────────────────────────────────────────────────────────

func checkServiceRole(r *http.Request) bool {
	if sqlRunnerInsecure() {
		return true // explicit bypass for debugging only
	}

	key := strings.TrimSpace(os.Getenv("SUPATYPE_SERVICE_ROLE_KEY"))
	if key == "" {
		return false // fail closed when key is missing
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	return token == key
}

func sqlRunnerInsecure() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(insecureEnv)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// ─── Connection pool ──────────────────────────────────────────────────────────

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error
)

func getPool(_ context.Context) (*pgxpool.Pool, error) {
	poolOnce.Do(func() {
		dsn := os.Getenv("SUPATYPE_SQL_DATABASE_URL")
		if dsn == "" {
			dsn = os.Getenv("DATABASE_URL")
		}
		if dsn == "" {
			poolErr = errNoDSN
			return
		}
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			poolErr = err
			return
		}
		cfg.MaxConns = 5
		// Use background context — pool lifetime must not be tied to the
		// first request's context.
		pool, poolErr = pgxpool.ConnectConfig(context.Background(), cfg)
		if poolErr != nil {
			logrus.WithError(poolErr).WithField("dsn_host", cfg.ConnConfig.Host).Error("sqlrunner: failed to connect pool")
		} else {
			logrus.WithField("dsn_host", cfg.ConnConfig.Host).Info("sqlrunner: pool connected")
		}
	})
	return pool, poolErr
}

var errNoDSN = &dsnError{}

type dsnError struct{}

func (e *dsnError) Error() string { return "DATABASE_URL is not set" }

// ─── Helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
