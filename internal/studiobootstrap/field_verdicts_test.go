package studiobootstrap

import "testing"

// Studio renders from these three answers, so each has to be right for a different reason:
// `read` decides lock-versus-value, `write` decides disabled-versus-editable, and `create`
// decides present-versus-absent in a create form.

func snapshotWithFields(fields string) *Snapshot {
	return &Snapshot{AST: []byte(`{"models":[{"name":"Doc","annotations":{
	  "db":{"tableName":"docs"},
	  "platform":{"access":{"fields":` + fields + `}}}}]}`)}
}

func verdicts(t *testing.T, fields string, caller Caller) map[string]FieldVerdict {
	t.Helper()
	tables, err := FieldVerdictsForCaller(snapshotWithFields(fields), caller)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return tables["docs"]
}

// A rule naming an application role is settled before any row is fetched: an admin gets the
// value, everyone else gets a lock.
func TestRoleRuleIsSettledPerCaller(t *testing.T) {
	fields := `{"secret":{"read":{"type":"role","roles":["admin"]}}}`

	admin := verdicts(t, fields, Caller{AppRole: "admin"})
	if admin["secret"].Read != VerdictAllow {
		t.Errorf("an admin should read it outright, got %q", admin["secret"].Read)
	}

	other := verdicts(t, fields, Caller{AppRole: "authenticated"})
	if other["secret"].Read != VerdictDeny {
		t.Errorf("a non-admin should be denied outright, got %q", other["secret"].Read)
	}
}

// An ownership rule cannot be settled without a row, so Studio must ask per row rather than
// assume either way.
func TestOwnershipRuleIsUnresolvedWithoutARow(t *testing.T) {
	got := verdicts(t, `{"secret":{"read":{"type":"owner","field":"owner_id"}}}`, Caller{UserID: "u1"})
	if got["secret"].Read != VerdictRow {
		t.Errorf("ownership depends on the row, got %q", got["secret"].Read)
	}
}

// No read rule means the column is as readable as the table.
func TestAbsentReadRuleAllows(t *testing.T) {
	got := verdicts(t, `{"tier":{"write":{"type":"role","roles":["admin"]}}}`, Caller{AppRole: "user"})
	if got["tier"].Read != VerdictAllow {
		t.Errorf("a write-only rule leaves reads alone, got %q", got["tier"].Read)
	}
	if got["tier"].Write != VerdictDeny {
		t.Errorf("a non-admin may not write it, got %q", got["tier"].Write)
	}
}

// Write is conjoined with read, mirroring what the engine bakes into the generated predicate.
// Being unable to read a column means being unable to write it, which is what makes
// write-without-read unrepresentable.
func TestWriteIsConjoinedWithRead(t *testing.T) {
	fields := `{"secret":{
	  "read":{"type":"role","roles":["admin"]},
	  "write":{"type":"public"}}}`

	got := verdicts(t, fields, Caller{AppRole: "user"})
	if got["secret"].Write != VerdictDeny {
		t.Errorf("a caller who cannot read it must not be told they can write it, got %q", got["secret"].Write)
	}

	admin := verdicts(t, fields, Caller{AppRole: "admin"})
	if admin["secret"].Write != VerdictAllow {
		t.Errorf("an admin reads and writes it, got %q", admin["secret"].Write)
	}
}

// The case that makes `create` a separate answer. On INSERT there is no row, so a
// row-dependent write predicate yields NULL for every caller — settled deny, not "ask per
// row". This is what lets a create form omit the field instead of rendering an input nobody
// can satisfy.
func TestRowDependentWriteCannotBeCreated(t *testing.T) {
	got := verdicts(t, `{"owner_id":{"write":{"type":"owner","field":"owner_id"}}}`, Caller{UserID: "u1"})

	if got["owner_id"].Create != VerdictDeny {
		t.Errorf("a row-dependent write rule is unsatisfiable on INSERT, got %q", got["owner_id"].Create)
	}
	if got["owner_id"].Write != VerdictRow {
		t.Errorf("the same rule is satisfiable on UPDATE by the owner, got %q", got["owner_id"].Write)
	}
}

// An identity-only write rule is satisfiable on INSERT, so `create` tracks `write` there.
func TestIdentityOnlyWriteCanBeCreated(t *testing.T) {
	admin := verdicts(t, `{"tier":{"write":{"type":"role","roles":["admin"]}}}`, Caller{AppRole: "admin"})
	if admin["tier"].Create != VerdictAllow {
		t.Errorf("an admin can set it at creation time, got %q", admin["tier"].Create)
	}
}

// A denied write stays denied on create; there is nothing weaker to fall back to.
func TestDeniedWriteIsDeniedOnCreate(t *testing.T) {
	got := verdicts(t, `{"locked":{"write":{"type":"private"}}}`, Caller{AppRole: "admin"})
	if got["locked"].Write != VerdictDeny || got["locked"].Create != VerdictDeny {
		t.Errorf("a private write rule denies both paths, got %+v", got["locked"])
	}
}

// With no write rule the governing predicate is the read rule, so a row-dependent *read* rule
// also makes the column uncreatable — the extension uses `can_read` as the write test there.
func TestAbsentWriteRuleInheritsTheReadRulesCreatability(t *testing.T) {
	got := verdicts(t, `{"secret":{"read":{"type":"owner","field":"owner_id"}}}`, Caller{UserID: "u1"})
	if got["secret"].Create != VerdictDeny {
		t.Errorf("a row-dependent read rule governs writing when no write rule exists, got %q", got["secret"].Create)
	}
}

// Models with no field rules contribute nothing, so Studio can treat an absent table as
// "no restrictions" rather than having to distinguish empty from missing.
func TestModelsWithoutFieldRulesAreOmitted(t *testing.T) {
	snapshot := &Snapshot{AST: []byte(`{"models":[{"name":"Plain","annotations":{
	  "db":{"tableName":"plain"},"platform":{"access":{"read":{"type":"public"}}}}}]}`)}
	tables, err := FieldVerdictsForCaller(snapshot, Caller{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, ok := tables["plain"]; ok {
		t.Error("a model with no field rules must not appear")
	}
}
