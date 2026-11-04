package changelog

import (
	"context"
	"encoding/json"

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

// renderQuery renders each input row and keeps only the columns that row carried.
//
// json_populate_record fills every column of the table, and a row from the change log may carry
// fewer: an old row under REPLICA IDENTITY DEFAULT is just its key, and an update leaves out
// unchanged TOASTed values. A hook should see what the change carried, not nulls standing in for
// what it did not. One output row per input element, in input order, by WITH ORDINALITY.
func renderQuery(schema, table string) string {
	return `SELECT COALESCE((
			SELECT json_object_agg(_supatype_col.key, _supatype_col.value)
			FROM json_each(row_to_json(_supatype_row)) AS _supatype_col
			WHERE _supatype_col.key IN (SELECT json_object_keys(_supatype_in.v))
		), '{}'::json)
		FROM json_array_elements($1::json) WITH ORDINALITY AS _supatype_in(v, n),
		LATERAL json_populate_record(NULL::` + pgx.Identifier{schema, table}.Sanitize() + `, _supatype_in.v) AS _supatype_row
		ORDER BY _supatype_in.n`
}

// render sends the rows as one JSON parameter and reads each rendered row back. pgx encodes the
// parameter and decodes the json result, and CollectRows reports a failure of either, or of the
// query, as one error. json.RawMessage keeps every value's bytes exactly as Postgres wrote them.
func (p pgRenderer) render(ctx context.Context, schema, table string, rows []map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	result, _ := p.conn.Query(ctx, renderQuery(schema, table), rows)
	return pgx.CollectRows(result, pgx.RowTo[map[string]json.RawMessage])
}
