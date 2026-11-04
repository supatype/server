package modelhooks

import (
	"encoding/json"

	"github.com/supatype/server/internal/proxy"
)

// ColumnsAll is the per-event opt-in that shows an after hook every column, restricted ones
// included. Anything else, a missing entry included, means "safe".
const ColumnsAll = "all"

// TableShape is what after hooks need to know about a table's columns.
//
// Read through ShapeFunc rather than straight off the manifest, so the source changes in one place.
type TableShape struct {
	// Columns maps an after event to "all" or "safe". Missing means "safe".
	Columns map[string]string
	// RestrictedColumns are the columns with a field read rule. Left out of a hook's rows unless the
	// event opted in, so a hook that forwards a row to email or chat cannot leak a column the caller
	// could never read.
	RestrictedColumns []string
	// PrimaryKey is the table's key, in order.
	PrimaryKey []string
}

// ShapeFunc returns a table's shape, and false when the listener and the fallback must leave the
// table's after hooks as they were before shapes existed. See ShapesFromManifest.
type ShapeFunc func(table string) (TableShape, bool)

// Strip returns a copy of row without the restricted columns, unless event opted in.
//
// A copy, because the same decoded row can be shown to more than one consumer.
func (s TableShape) Strip(event string, row map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(row))
	for name, value := range row {
		out[name] = value
	}
	if s.Columns[event] == ColumnsAll {
		return out
	}
	for _, name := range s.RestrictedColumns {
		delete(out, name)
	}
	return out
}

// StripAll strips every row for event, keeping nil as nil.
func (s TableShape) StripAll(event string, rows []map[string]json.RawMessage) []map[string]json.RawMessage {
	if rows == nil {
		return nil
	}
	out := make([]map[string]json.RawMessage, len(rows))
	for i, r := range rows {
		out[i] = s.Strip(event, r)
	}
	return out
}

// ShapesFromManifest reads table shapes from the manifest's top-level `tables` map, the one
// accessor every after-hook path uses.
func ShapesFromManifest(m *proxy.RouteManifest) ShapeFunc {
	return func(table string) (TableShape, bool) {
		meta, ok := m.Table(table)
		if !ok {
			return shapeWithoutTablesMap(table)
		}
		return TableShape{
			Columns:           meta.Columns,
			RestrictedColumns: meta.RestrictedColumns,
			PrimaryKey:        meta.PrimaryKey,
		}, true
	}
}

// shapeWithoutTablesMap is the whole policy for a manifest pushed by a CLI that predates the
// `tables` map, or a hooked table missing from it.
//
// OPEN DECISION (reported to Nic, not chosen here): without `restrictedColumns` nothing says which
// columns to strip. Until it is decided this keeps the behaviour the released server already has:
// the table is not handled by the change-log listener, and its after hook stays on the request path
// with the rows PostgREST returned to the caller, which PostgREST has already masked for that
// caller. That is no worse than today and leaks nothing new. Change it here and only here.
func shapeWithoutTablesMap(string) (TableShape, bool) {
	return TableShape{}, false
}
