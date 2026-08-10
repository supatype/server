package studiobootstrap

import "encoding/json"

// FieldVerdict is what this caller may do with one column, resolved as far as it can be
// without a row.
//
// Three answers rather than one because the three paths genuinely differ. The clearest case
// is a required column with an ownership write rule: readable, updatable by its owner, and
// creatable by nobody — so a create form that renders it as a required input is asking for
// something no caller can supply.
type FieldVerdict struct {
	Read Verdict `json:"read"`
	// Write is the UPDATE path.
	Write Verdict `json:"write"`
	// Create is the INSERT path, which is stricter: there is no row yet, so the masking
	// extension evaluates the write predicate against `NULL::t` and a rule that reads the
	// row yields NULL for every caller.
	Create Verdict `json:"create"`
}

// FieldVerdictsForCaller resolves every field rule against this caller, per table.
//
// Studio's use for it is not decoration: a masked cell should show a lock rather than an
// empty string, a readable-but-not-writable input should be disabled rather than silently
// rejected on save, and a required column nobody can create should be absent from a create
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

	// No write rule means writable exactly when readable — the same invariant the engine
	// bakes in by conjoining a field's write rule with its read rule, which is what makes
	// write-without-read unrepresentable.
	write := read
	governing := readRule
	if len(writeRule) > 0 {
		write = conjoin(Evaluate(writeRule, caller), read)
		governing = writeRule
	}

	create := write
	if write != VerdictDeny && IsRowDependent(governing) {
		// Unsatisfiable on INSERT for every caller, so this is settled rather than "ask per
		// row" — which is exactly what lets a create form leave the field out.
		create = VerdictDeny
	}

	return FieldVerdict{Read: read, Write: write, Create: create}
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
