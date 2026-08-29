// Package sqlrunner provides the Studio SQL-runner HTTP handler.
//
// Route (requires a service-role Bearer token unless explicitly insecure):
//
//	POST /sql   — execute a SQL query and return rows as JSON
//
// The Postgres search_path is decided server-side from the JWT role claim.
// Clients may request a schema override in the body, but it is only honoured
// when the JWT role is "service_role" or the explicit insecure switch is on.
package sqlrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/modes"
	"github.com/supatype/server/internal/utilities"
)

const (
	queryTimeout = 30 * time.Second
	maxRows      = 10_000
	// fallbackSchema is used when neither configuration nor the request names one.
	fallbackSchema = "public"
)

// Pool is the part of a connection pool this package uses. Narrowed to the one
// method so the handler can be exercised without a database, and so nothing
// here can reach past a transaction.
type Pool interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Pools obtains the pool at request time rather than at mount time, because a
// deployment may be configured with no database and must answer 503 rather
// than refuse to start.
type Pools func() (Pool, error)

// Row is one result row: column name to value. The values are whatever the
// query selected, so their types are Postgres's to decide, not this package's.
type Row map[string]any

// Response is what a successful run returns.
type Response struct {
	Rows     []Row  `json:"rows"`
	RowCount int    `json:"rowCount"`
	Schema   string `json:"schema"`
}

// Handler returns the SQL runner endpoint.
func Handler(cfg *config.Config, pools Pools) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		if !checkServiceRole(cfg, r) {
			writeError(w, http.StatusUnauthorized, errors.New("service role key required"))
			return
		}

		query, requested, ok := decodeRequest(r)
		if !ok {
			writeError(w, http.StatusBadRequest, errors.New("query is required"))
			return
		}

		pool, err := pools()
		if err != nil {
			logrus.WithError(err).Error("sqlrunner: database not available")
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("database not available: %w", err))
			return
		}

		schema := resolveSchema(cfg, r.Header.Get("Authorization"), requested)
		rows, err := run(r.Context(), pool, schema, query)
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		utilities.WriteJSON(w, http.StatusOK, Response{Rows: rows, RowCount: len(rows), Schema: schema})
	})
}

// decodeRequest reads the body. A query of nothing but whitespace is the same
// as no query at all.
func decodeRequest(r *http.Request) (query, schema string, ok bool) {
	var body struct {
		Query  string `json:"query"`
		Schema string `json:"schema,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return "", "", false
	}
	if strings.TrimSpace(body.Query) == "" {
		return "", "", false
	}
	return body.Query, body.Schema, true
}

// ─── Execution ────────────────────────────────────────────────────────────────

// failure carries the status a problem should be reported as, so each place
// that can fail says what it means rather than leaving a later switch to guess
// from the error text.
type failure struct {
	status int
	err    error
}

func (f failure) Error() string { return f.err.Error() }
func (f failure) Unwrap() error { return f.err }

func failing(status int, format string, args ...any) error {
	return failure{status: status, err: fmt.Errorf(format, args...)}
}

// statusFor is how a failure reaches the response. Anything unclassified is
// ours, not the caller's.
func statusFor(err error) int {
	var f failure
	if errors.As(err, &f) {
		return f.status
	}
	return http.StatusInternalServerError
}

// run executes one query with the search_path scoped to this request.
//
// The transaction is what scopes it: `set_config(..., is_local => true)` is
// SET LOCAL, so the search_path cannot leak to the next request that borrows
// this connection from the pool.
func run(ctx context.Context, pool Pool, schema, query string) ([]Row, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, failing(http.StatusServiceUnavailable, "failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, "SELECT pg_catalog.set_config('search_path', $1, true)", schema); err != nil {
		return nil, failing(http.StatusInternalServerError, "failed to set schema: %w", err)
	}

	rows, err := collect(ctx, tx, query)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, failing(http.StatusInternalServerError, "failed to commit: %w", err)
	}
	return rows, nil
}

// collect reads the result set, refusing one too large to hold in memory.
func collect(ctx context.Context, tx pgx.Tx, query string) ([]Row, error) {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		logrus.WithError(err).Error("sqlrunner: query failed")
		return nil, failure{status: http.StatusUnprocessableEntity, err: err}
	}
	defer rows.Close()

	columns := make([]string, len(rows.FieldDescriptions()))
	for i, fd := range rows.FieldDescriptions() {
		columns[i] = fd.Name
	}

	result := make([]Row, 0)
	for rows.Next() {
		if len(result) >= maxRows {
			return nil, failing(http.StatusRequestEntityTooLarge, "result exceeds %d row limit", maxRows)
		}
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(Row, len(columns))
		for i, column := range columns {
			row[column] = values[i]
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// ─── Schema resolution ────────────────────────────────────────────────────────

var validIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// resolveSchema decides the Postgres schema for a request.
//
//   - With the insecure switch on, any valid requested schema is accepted.
//   - For a service_role JWT the client may request an override.
//   - Every other role is locked to the configured schema.
func resolveSchema(cfg *config.Config, authHeader, requested string) string {
	fallback := strings.TrimSpace(cfg.SQLSchema)
	if fallback == "" {
		fallback = fallbackSchema
	}
	if requested == "" || !validIdentifier.MatchString(requested) {
		return fallback
	}
	if cfg.SQLRunnerInsecure.Bool() || jwtRole(authHeader) == "service_role" {
		return requested
	}
	return fallback
}

// jwtRole reads the "role" claim from a Bearer JWT without verifying the
// signature. The signature is checkServiceRole's business; this only needs the
// plaintext claim to route the schema, and answers "anon" for anything it
// cannot read.
func jwtRole(authHeader string) string {
	token, _ := strings.CutPrefix(authHeader, "Bearer ")
	parts := strings.Split(token, ".")
	if token == "" || len(parts) != 3 {
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

// checkServiceRole gates the SQL runner.
//
// Unlike the functions and admin APIs, it has no dev-mode bypass: dev mode
// alone must not open arbitrary SQL execution. The only bypass is the explicit
// insecure switch, and it fails closed when no key is configured.
func checkServiceRole(cfg *config.Config, r *http.Request) bool {
	if cfg.SQLRunnerInsecure.Bool() {
		return true // explicit bypass for debugging only
	}
	return modes.ServiceRoleBearer(r, cfg.ServiceRoleKey)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func writeError(w http.ResponseWriter, status int, err error) {
	utilities.WriteJSON(w, status, map[string]string{"error": err.Error()})
}
