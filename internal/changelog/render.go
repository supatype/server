package changelog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// pgRenderer renders rows with Postgres itself, so a hook sees each value exactly as PostgREST
// would return it for the same row.
//
// PostgREST renders a row with Postgres's own JSON functions, so the faithful copy of its output is
// to hand the decoded values back to Postgres: json_populate_record reads each one through the
// column type's input function, and row_to_json writes it the way PostgREST does. Timestamps become
// ISO strings, arrays JSON arrays, composites objects, ranges and intervals their text, numerics
// keep their digits. Doing that conversion in Go instead would mean re-implementing the output of
// every type Postgres has, extension types included.
type pgRenderer struct {
	conn *pgx.Conn
}

func renderQuery(schema, table string) string {
	return `SELECT row_to_json(_supatype_row)::text
		FROM json_array_elements($1::json) WITH ORDINALITY AS _supatype_in(v, n),
		LATERAL json_populate_record(NULL::` + pgx.Identifier{schema, table}.Sanitize() + `, _supatype_in.v) AS _supatype_row
		ORDER BY _supatype_in.n`
}

func (p pgRenderer) render(ctx context.Context, schema, table string, rows []map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	input, err := json.Marshal(rows)
	if err != nil {
		return nil, err
	}
	result, err := p.conn.Query(ctx, renderQuery(schema, table), string(input))
	if err != nil {
		return nil, err
	}
	defer result.Close()
	out := make([]map[string]json.RawMessage, 0, len(rows))
	for result.Next() {
		var text string
		if err := result.Scan(&text); err != nil {
			return nil, err
		}
		var row map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := result.Err(); err != nil {
		return nil, err
	}
	return keepColumns(rows, out)
}

// keepColumns drops the columns rendering added. json_populate_record fills every column of the
// table, and a row from the change log may carry fewer: an old row under REPLICA IDENTITY DEFAULT
// is just its key, and an update leaves out unchanged TOASTed values. A hook should see what the
// change carried, not nulls standing in for what it did not.
func keepColumns(in, rendered []map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	if len(in) != len(rendered) {
		return nil, fmt.Errorf("rendered %d rows from %d", len(rendered), len(in))
	}
	for i, row := range rendered {
		for name := range row {
			if _, ok := in[i][name]; !ok {
				delete(row, name)
			}
		}
	}
	return rendered, nil
}
