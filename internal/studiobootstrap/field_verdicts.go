package studiobootstrap

import "encoding/json"

// FieldVerdict is what this caller may do with one column, resolved as far as it can be
// without a row.
//
// Three answers rather than one because the paths are judged against different rows. The
// clearest case is a required column whose only rule is an ownership read rule: on an existing
// record a caller who does not own it gets a padlock and cannot change it, while on a new record
// the rule is judged against the row being stored, so the form must still ask for the value.
type FieldVerdict struct {
	Read Verdict `json:"read"`
	// Write is the UPDATE path.
	Write Verdict `json:"write"`
	// Create is the INSERT path. The masking extension (MASK-N1) and the tier 2 view trigger
	// both judge the write predicate against the new row, as an RLS `WITH CHECK` does, so a
	// rule that reads the row is answered per row here too, never refused outright.
	Create Verdict `json:"create"`
}

// FieldVerdictsForCaller resolves every field rule against this caller, per table.
//
// Studio's use for it is not decoration: a masked cell should show a lock rather than an
// empty string, a readable-but-not-writable input should be disabled rather than silently
// rejected on save, and a column no caller could ever create should be absent from a create
// form rather than block it. All three need the answer before any row is fetched.
//
// Advisory, like everything else Studio is told. The enforcement is the security labels and
// the planner rewrite; a tampered response can only produce an interface that asks for
// something the database then refuses.
func FieldVerdictsForCaller(
	snapshot *Snapshot,
	caller Caller,
) (map[string]map[string]FieldVerdict, error) {
	var shape fieldAstShape
	if err := json.Unmarshal(snapshot.AST, &shape); err != nil {
		return nil, err
	}

	tables := make(map[string]map[string]FieldVerdict, len(shape.Models))
	for _, m := range shape.Models {
		if len(m.Annotations.Platform.Access.Fields) == 0 {
			continue
		}

		table := m.Annotations.DB.TableName
		if table == "" {
			table = m.Name
		}

		fields := make(map[string]FieldVerdict, len(m.Annotations.Platform.Access.Fields))
		for column, rules := range m.Annotations.Platform.Access.Fields {
			fields[column] = resolveField(rules.Read, rules.Write, caller)
		}
		tables[table] = fields
	}

	return tables, nil
}

func resolveField(readRule, writeRule json.RawMessage, caller Caller) FieldVerdict {
	// No read rule means the column is as readable as the table itself.
	read := VerdictAllow
	if len(readRule) > 0 {
		read = Evaluate(readRule, caller)
	}

	// No write rule means writable exactly when readable: the same invariant the engine
	// bakes in by conjoining a field's write rule with its read rule, which is what makes
	// write-without-read unrepresentable.
	write := read
	if len(writeRule) > 0 {
		write = conjoin(Evaluate(writeRule, caller), read)
	}

	// INSERT runs the same predicate as UPDATE, against the new row instead of the stored
	// one. A rule that reads the row is therefore `row` on both paths: the caller may create
	// a record their rule accepts, such as one they own, and the database refuses the rest.
	return FieldVerdict{Read: read, Write: write, Create: write}
}

// conjoin is `AND` over verdicts: certain when both are certain, denied if either denies, and
// otherwise unresolved.
func conjoin(a, b Verdict) Verdict {
	if a == VerdictDeny || b == VerdictDeny {
		return VerdictDeny
	}
	if a == VerdictAllow && b == VerdictAllow {
		return VerdictAllow
	}
	return VerdictRow
}
