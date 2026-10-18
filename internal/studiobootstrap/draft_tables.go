package studiobootstrap

import (
	"context"
	"encoding/json"
	"time"

	"github.com/supatype/server/internal/data"
)

// draftTablesTTL bounds how stale the classification may be, for the same reason as
// identityScopeTTL: it is consulted on a request path and changes only on a push.
const draftTablesTTL = 30 * time.Second

var draftTables = classification[map[string]bool]{ttl: draftTablesTTL}

// DraftTables returns the tables of models that keep drafts: `versions` with drafts on.
//
// A new record on such a model is unpublished, so a plain insert that asks for the row
// back is refused when the read rule hides unpublished records. The REST gateway uses this
// to say so on that refusal, and to name `supatype.create_record`, which creates the record
// as its first draft.
//
// The second result is false when the classification could not be read.
func DraftTables(ctx context.Context, resources *data.Resources) (map[string]bool, bool) {
	return draftTables.get(ctx, resources, draftTablesFromAST)
}

// draftsAstShape reads what [astShape] does not model: a model's `options.versions`.
type draftsAstShape struct {
	Models []struct {
		Name        string `json:"name"`
		Annotations struct {
			DB struct {
				TableName string `json:"tableName"`
			} `json:"db"`
		} `json:"annotations"`
		Options struct {
			Versions json.RawMessage `json:"versions"`
		} `json:"options"`
	} `json:"models"`
}

// draftTablesFromAST is the pure half: snapshot bytes in, the drafts tables out.
func draftTablesFromAST(raw []byte) (map[string]bool, error) {
	var shape draftsAstShape
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, err
	}

	tables := make(map[string]bool)
	for _, m := range shape.Models {
		if !keepsDrafts(m.Options.Versions) {
			continue
		}
		table := m.Annotations.DB.TableName
		if table == "" {
			table = m.Name
		}
		tables[table] = true
	}
	return tables, nil
}

// keepsDrafts reads a model's `versions` as the engine does: `true` is drafts with the
// default retention, an object keeps drafts unless it says `drafts: false`, and anything
// else (absent, `false`, null) is no versions at all.
func keepsDrafts(raw json.RawMessage) bool {
	var flag bool
	if json.Unmarshal(raw, &flag) == nil {
		return flag
	}
	var options struct {
		Drafts *bool `json:"drafts"`
	}
	if json.Unmarshal(raw, &options) != nil {
		return false
	}
	return options.Drafts == nil || *options.Drafts
}
