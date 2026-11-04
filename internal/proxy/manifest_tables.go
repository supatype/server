package proxy

import "slices"

// TableMeta is what `supatype push` records about one table itself, as opposed to the hooks,
// validators or cache declared on it. See RouteManifest.Tables.
type TableMeta struct {
	// Columns says, per after event ("afterChange", "afterDelete"), whether a hook receives every
	// column ("all") or only the unrestricted ones ("safe"). A missing event means "safe".
	Columns map[string]string `json:"columns,omitempty"`

	// RestrictedColumns lists every column that has a field read rule.
	RestrictedColumns []string `json:"restrictedColumns,omitempty"`

	// PrimaryKey is the table's key columns, in order.
	PrimaryKey []string `json:"primaryKey,omitempty"`
}

// Table returns what the manifest records about table. Safe on a nil manifest.
func (m *RouteManifest) Table(table string) (TableMeta, bool) {
	if m == nil {
		return TableMeta{}, false
	}
	meta, ok := m.Tables[table]
	return meta, ok
}

// PrimaryKey returns table's key columns, or nil when the manifest does not record them.
func (m *RouteManifest) PrimaryKey(table string) []string {
	meta, _ := m.Table(table)
	return slices.Clone(meta.PrimaryKey)
}

// cloneTables deep-copies the map for CloneRouteManifest, so one tenant's reload cannot change
// what another tenant's in-flight request is reading.
func cloneTables(tables map[string]TableMeta) map[string]TableMeta {
	if len(tables) == 0 {
		return nil
	}
	out := make(map[string]TableMeta, len(tables))
	for table, meta := range tables {
		columns := make(map[string]string, len(meta.Columns))
		for event, mode := range meta.Columns {
			columns[event] = mode
		}
		out[table] = TableMeta{
			Columns:           columns,
			RestrictedColumns: slices.Clone(meta.RestrictedColumns),
			PrimaryKey:        slices.Clone(meta.PrimaryKey),
		}
	}
	return out
}

// mergeTables takes the overlay's map wholesale, on the same terms as Hooks: it comes from the same
// push, and a per-table merge would keep a dropped table's facts alive.
func mergeTables(base, overlay *RouteManifest) {
	if overlay.Tables != nil {
		base.Tables = overlay.Tables
	}
}
