package sqlrunner

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/utilities"
)

// Studio's schema metadata (RQ3): a fixed catalogue of named, read-only
// queries over catalogs and the engine's ledger, for every Studio role holding
// schema view, viewers included.
//
// Studio's Database views used to send their own SQL to the runner, which only
// owners, admins and developers may use, so a viewer's every Database view
// failed. Opening the runner wider would hand anyone with schema view a way to
// read every row. Instead the caller names a query from studio_metadata.json,
// the same document the control plane carries, and passes its parameters,
// which are bound rather than written into the statement. Nothing a caller
// sends becomes SQL.
//
//	POST /metadata   {"name": "...", "params": {...}}

//go:embed studio_metadata.json
var metadataDocument []byte

type metadataQuery struct {
	Params []string `json:"params"`
	SQL    string   `json:"sql"`
}

type metadataDoc struct {
	Queries map[string]metadataQuery `json:"queries"`
}

var metadataCatalogue = mustLoadMetadata(metadataDocument)

// mustLoadMetadata parses the embedded catalogue. A document that does not
// parse stops the server at start rather than serving no Database views.
func mustLoadMetadata(raw []byte) metadataDoc {
	var doc metadataDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		panic(fmt.Sprintf("sqlrunner: studio_metadata.json: %v", err))
	}
	return doc
}

// metadataStatement is the query a request names with its parameters in
// order, or why there is none: an unknown name, a missing parameter, or a
// parameter that is not a plain value.
func metadataStatement(name string, params map[string]any) (string, []any, error) {
	query, ok := metadataCatalogue.Queries[name]
	if !ok {
		return "", nil, fmt.Errorf("unknown metadata query: %s", name)
	}
	values := make([]any, 0, len(query.Params))
	for _, param := range query.Params {
		value, present := params[param]
		if !present || !plainValue(value) {
			return "", nil, fmt.Errorf("metadata query %q needs %q", name, param)
		}
		values = append(values, value)
	}
	return query.SQL, values, nil
}

func plainValue(value any) bool {
	switch value.(type) {
	case nil, string, bool, float64:
		return true
	default:
		return false
	}
}

// MetadataHandler serves POST /metadata, behind the same service-role gate as
// the runner: Studio reaches it through its proxy, which admits schema view.
func MetadataHandler(cfg *config.Config, pools Pools) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
			return
		}
		if !checkServiceRole(cfg, r) {
			writeError(w, http.StatusUnauthorized, errors.New("service role key required"))
			return
		}
		var body struct {
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, errors.New("a metadata query name is required"))
			return
		}
		sql, values, err := metadataStatement(body.Name, body.Params)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		answerMetadata(w, r.Context(), pools, sql, values)
	})
}

func answerMetadata(w http.ResponseWriter, ctx context.Context, pools Pools, sql string, values []any) {
	pool, err := pools()
	if err != nil {
		logrus.WithError(err).Error("sqlrunner: database not available")
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("database not available: %w", err))
		return
	}
	rows, err := runReadOnly(ctx, pool, sql, values)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	utilities.WriteJSON(w, http.StatusOK, Response{Rows: rows, RowCount: len(rows)})
}

// runReadOnly runs one bound statement in a read-only transaction, so nothing
// it does can change the database.
func runReadOnly(ctx context.Context, pool Pool, sql string, values []any) ([]Row, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, failing(http.StatusServiceUnavailable, "failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
		return nil, failing(http.StatusInternalServerError, "failed to make the transaction read-only: %w", err)
	}
	return collect(ctx, tx, sql, values...)
}
