package studiobootstrap

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/supatype/server/internal/data"
)

// IsRowDependent reports whether a rule's outcome varies by *which row* it is tested
// against.
//
// A different question from [IsIdentityDependent], and the two are easy to conflate
// because the commonest rule is both. `Role<"admin">` varies by caller and not by row;
// `Lte<"published_at", Now>` varies by row and not by caller; `Owner<"author_id">` varies
// by both.
//
// It matters here because a masked-field header is computed before the query runs. For a
// rule that does not read the row, "masked" is a property of the whole result set. For one
// that does, the header can only say "may be masked in some rows" — and a client that
// treated that as exact would hide values the caller was entitled to see.
func IsRowDependent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var r rule
	if err := json.Unmarshal(raw, &r); err != nil {
		// An unreadable rule is assumed row-dependent. That only ever makes the header
		// less precise, never wrong in the direction that hides data.
		return true
	}
	return ruleIsRowDependent(&r)
}

func ruleIsRowDependent(r *rule) bool {
	switch r.Type {
	case "public", "private", "authenticated", "role":
		return false

	// Compares a column of this row to the caller.
	case "owner":
		return true

	// Correlates a column of this row against another table.
	case "in", "exists":
		return true

	case "compare":
		return operandIsRowDependent(r.Left) || operandIsRowDependent(r.Right)

	case "nullCheck":
		return operandIsRowDependent(r.Operand)

	case "any", "all":
		for i := range r.Rules {
			if ruleIsRowDependent(&r.Rules[i]) {
				return true
			}
		}
		return false

	case "not":
		return r.Rule != nil && ruleIsRowDependent(r.Rule)

	default:
		return true
	}
}

func operandIsRowDependent(o *operand) bool {
	return o != nil && o.Kind == "column"
}

// FieldMask is one column's read restriction, as far as it can be known before the query
// runs.
type FieldMask struct {
	Column string `json:"column"`
	// RowDependent means the verdict varies row by row, so the header can only warn.
	RowDependent bool `json:"rowDependent"`
}

// fieldScopeTTL bounds how stale the classification may be. Same reasoning as
// identityScopeTTL: it is consulted per request and only changes on a schema push.
const fieldScopeTTL = 30 * time.Second

var fieldScope struct {
	sync.Mutex
	tables   map[string][]FieldMask
	loadedAt time.Time
	loaded   bool
}

// MaskedFields returns, per table, the columns carrying a read restriction.
//
// Derived from the AST, so it costs no database work per request and no second
// implementation of the rules — the *enforcement* of these restrictions is the security
// labels and the planner rewrite, which this only describes.
//
// Caller-independent by design. It says which columns are restricted and whether the
// restriction varies by row, not whether *this* caller is masked: answering that would mean
// evaluating the predicate, and Studio already gets the exact per-caller answer from
// `/studio/session`. An app client wants to know which nulls are explicable, which this
// gives for free.
//
// The second result is false when the classification could not be read. Callers then send
// no header at all — an absent header means "not stated", whereas a wrong one would be
// taken as fact.
func MaskedFields(ctx context.Context, resources *data.Resources) (map[string][]FieldMask, bool) {
	fieldScope.Lock()
	defer fieldScope.Unlock()

	if fieldScope.loaded && time.Since(fieldScope.loadedAt) < fieldScopeTTL {
		return fieldScope.tables, true
	}

	snapshot, err := LoadSnapshot(ctx, resources)
	if err != nil {
		// Keep serving a previous answer rather than flapping on a transient blip; the
		// TTL still bounds how old it can be.
		if fieldScope.loaded {
			return fieldScope.tables, true
		}
		return nil, false
	}

	tables, err := maskedFieldsFromAST(snapshot.AST)
	if err != nil {
		return nil, false
	}

	fieldScope.tables = tables
	fieldScope.loadedAt = time.Now()
	fieldScope.loaded = true
	return tables, true
}

// maskedFieldsFromAST is the pure half: snapshot bytes in, classification out. Split out so
// the assumption this makes about the AST's shape can be tested against a real snapshot
// rather than only against a running database.
func maskedFieldsFromAST(raw []byte) (map[string][]FieldMask, error) {
	var shape fieldAstShape
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, err
	}

	tables := make(map[string][]FieldMask, len(shape.Models))
	for _, m := range shape.Models {
		table := m.Annotations.DB.TableName
		if table == "" {
			table = m.Name
		}

		var masks []FieldMask
		for column, rules := range m.Annotations.Platform.Access.Fields {
			if len(rules.Read) == 0 {
				// A write-only rule leaves reads alone, so there is nothing to warn about.
				continue
			}
			if isPublicRule(rules.Read) {
				// Labelled, but readable by everyone — noise in a header about masking.
				continue
			}
			masks = append(masks, FieldMask{
				Column:       column,
				RowDependent: IsRowDependent(rules.Read),
			})
		}
		if len(masks) > 0 {
			sortMasks(masks)
			tables[table] = masks
		}
	}

	return tables, nil
}

// isPublicRule reports whether a rule admits everyone unconditionally.
func isPublicRule(raw json.RawMessage) bool {
	var r rule
	if err := json.Unmarshal(raw, &r); err != nil {
		return false
	}
	return r.Type == "public"
}

// sortMasks keeps the header value stable across requests, so it does not defeat any
// downstream caching or make responses needlessly diff-noisy.
func sortMasks(masks []FieldMask) {
	for i := 1; i < len(masks); i++ {
		for j := i; j > 0 && masks[j].Column < masks[j-1].Column; j-- {
			masks[j], masks[j-1] = masks[j-1], masks[j]
		}
	}
}

// fieldAstShape reads the `fields` half of a model's access block, which [astShape] does
// not model.
type fieldAstShape struct {
	Models []struct {
		Name        string `json:"name"`
		Annotations struct {
			DB struct {
				TableName string `json:"tableName"`
			} `json:"db"`
			Platform struct {
				Access struct {
					Fields map[string]struct {
						Read  json.RawMessage `json:"read"`
						Write json.RawMessage `json:"write"`
					} `json:"fields"`
				} `json:"access"`
			} `json:"platform"`
		} `json:"annotations"`
	} `json:"models"`
}
