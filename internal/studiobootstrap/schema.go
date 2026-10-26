package studiobootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/supatype/server/internal/data"
)

// ErrNoSchemaState means the project has never been pushed, so there is nothing
// for Studio to render yet.
var ErrNoSchemaState = errors.New("no schema state: push the schema first")

const readTimeout = 5 * time.Second

// Snapshot is the schema as the last push recorded it, plus a hash to cache on.
type Snapshot struct {
	AST         json.RawMessage
	AdminConfig json.RawMessage
	// Hash covers the AST and the admin config, so a client's cached copy is
	// invalidated by any change to either. Keyed on content rather than a
	// timestamp: a push that changes nothing should not invalidate anything.
	Hash string
}

// LoadSnapshot reads the schema state written by the last push.
func LoadSnapshot(ctx context.Context, resources *data.Resources) (*Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	pool, err := resources.AdminPool()
	if err != nil {
		return nil, err
	}

	ast, adminConfig, err := readSchemaState(ctx, pool)
	if err != nil {
		return nil, err
	}
	if ast == "" {
		return nil, ErrNoSchemaState
	}
	return newSnapshot(ast, adminConfig), nil
}

// readSchemaState reads the stored AST and admin config as JSON text.
//
// Read as text, never cast to bytea: a text-to-bytea cast parses every backslash
// as a bytea escape, so any schema whose JSON quotes a string (a check
// expression, a TypeScript type) failed with "invalid input syntax for type
// bytea" and could not be loaded at all.
func readSchemaState(ctx context.Context, pool *pgxpool.Pool) (ast, adminConfig string, err error) {
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(ast_snapshot::text, ''),
		       COALESCE(admin_config::text, '')
		  FROM _supatype.schema_state
		 WHERE id = 1`).Scan(&ast, &adminConfig)
	if errors.Is(err, pgx.ErrNoRows) {
		// A project that has never been pushed has no row at all, which is the
		// same thing to Studio as a row with nothing in it. Returning the driver
		// error instead put "no rows in result set" in front of a developer on
		// their first run, where the answer is "push the schema first".
		return "", "", ErrNoSchemaState
	}
	return ast, adminConfig, err
}

// newSnapshot wraps the stored JSON with a hash of both halves.
func newSnapshot(ast, adminConfig string) *Snapshot {
	sum := sha256.New()
	sum.Write([]byte(ast))
	sum.Write([]byte(adminConfig))

	return &Snapshot{
		AST:         json.RawMessage(ast),
		AdminConfig: json.RawMessage(adminConfig),
		Hash:        hex.EncodeToString(sum.Sum(nil))[:32],
	}
}

// Model is one table as Studio needs to know about it.
type Model struct {
	Name  string `json:"name"`
	Table string `json:"table"`
	// Access is what the caller may do, resolved as far as it can be without a
	// row. `row` means Studio must ask per row rather than assume.
	Access map[string]Verdict `json:"access"`
}

// astShape is the part of the snapshot this package reads.
type astShape struct {
	Models []struct {
		Name        string `json:"name"`
		Annotations struct {
			DB struct {
				TableName string `json:"tableName"`
			} `json:"db"`
			Platform struct {
				Access map[string]json.RawMessage `json:"access"`
			} `json:"platform"`
		} `json:"annotations"`
	} `json:"models"`
}

// FilterForCaller returns the models this caller can do anything with at all.
//
// Filtered on the server, deliberately: sending the whole schema and hiding parts
// in the browser tells every caller what tables exist, which is information the
// access rules said they should not have. A model where every operation is denied
// is omitted entirely.
func FilterForCaller(snapshot *Snapshot, caller Caller) ([]Model, error) {
	var shape astShape
	if err := json.Unmarshal(snapshot.AST, &shape); err != nil {
		return nil, err
	}

	models := make([]Model, 0, len(shape.Models))
	for _, m := range shape.Models {
		access, reachable := modelAccess(m.Annotations.Platform.Access, caller)
		if !reachable {
			continue
		}

		table := m.Annotations.DB.TableName
		if table == "" {
			table = m.Name
		}
		models = append(models, Model{Name: m.Name, Table: table, Access: access})
	}
	return models, nil
}

// modelOperations is fixed in order so the response is stable and cacheable.
var modelOperations = []string{"read", "create", "update", "delete"}

// modelAccess is the caller's verdict on each operation of one model, and
// whether any of them is anything but a denial.
func modelAccess(rules map[string]json.RawMessage, caller Caller) (map[string]Verdict, bool) {
	access := make(map[string]Verdict, len(modelOperations))
	reachable := false
	for _, op := range modelOperations {
		v := Evaluate(rules[op], caller)
		if caller.AsStudio {
			v = VerdictAllow
		}
		access[op] = v
		if v != VerdictDeny {
			reachable = true
		}
	}
	return access, reachable
}
