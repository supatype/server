package studiobootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/supatype/server/internal/dbpool"
)

// ErrNoSchemaState means the project has never been pushed, so there is nothing
// for Studio to render yet.
var ErrNoSchemaState = errors.New("no schema state — push the schema first")

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
func LoadSnapshot(ctx context.Context) (*Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	pool, err := dbpool.Pool(ctx)
	if err != nil {
		return nil, err
	}

	var ast, adminConfig []byte
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(ast_snapshot::text, '')::bytea,
		       COALESCE(admin_config::text, '')::bytea
		  FROM _supatype.schema_state
		 WHERE id = 1`).Scan(&ast, &adminConfig)
	if err != nil {
		return nil, err
	}
	if len(ast) == 0 {
		return nil, ErrNoSchemaState
	}

	sum := sha256.New()
	sum.Write(ast)
	sum.Write(adminConfig)

	return &Snapshot{
		AST:         json.RawMessage(ast),
		AdminConfig: json.RawMessage(adminConfig),
		Hash:        hex.EncodeToString(sum.Sum(nil))[:32],
	}, nil
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

	// Fixed order so the response is stable and cacheable.
	operations := []string{"read", "create", "update", "delete"}

	models := make([]Model, 0, len(shape.Models))
	for _, m := range shape.Models {
		access := make(map[string]Verdict, len(operations))
		reachable := false
		for _, op := range operations {
			v := Evaluate(m.Annotations.Platform.Access[op], caller)
			access[op] = v
			if v != VerdictDeny {
				reachable = true
			}
		}
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
