package studiobootstrap

import (
	"encoding/json"
	"testing"
)

func verdict(t *testing.T, ruleJSON string, caller Caller) Verdict {
	t.Helper()
	return Evaluate(json.RawMessage(ruleJSON), caller)
}

var admin = Caller{UserID: "u1", AppRole: "admin"}
var reader = Caller{UserID: "u2", AppRole: "authenticated"}
var anon = Caller{AppRole: "anon"}

func TestSimpleRules(t *testing.T) {
	cases := []struct {
		rule   string
		caller Caller
		want   Verdict
	}{
		{`{"type":"public"}`, anon, VerdictAllow},
		{`{"type":"private"}`, admin, VerdictDeny},
		{`{"type":"authenticated"}`, reader, VerdictAllow},
		{`{"type":"authenticated"}`, anon, VerdictDeny},
		{`{"type":"role","roles":["admin"]}`, admin, VerdictAllow},
		{`{"type":"role","roles":["admin"]}`, reader, VerdictDeny},
		{`{"type":"role","roles":["editor","admin"]}`, admin, VerdictAllow},
	}
	for _, c := range cases {
		if got := verdict(t, c.rule, c.caller); got != c.want {
			t.Errorf("%s as %q: got %q, want %q", c.rule, c.caller.AppRole, got, c.want)
		}
	}
}

// An absent rule is a denial. Deny-by-default is the premise of the whole design,
// so a missing rule must never read as permission.
func TestAbsentRuleDenies(t *testing.T) {
	if got := Evaluate(nil, admin); got != VerdictDeny {
		t.Fatalf("got %q, want deny", got)
	}
}

// Anything about a particular row is reported as such rather than guessed — an
// honest "it depends" is what lets Studio ask per row; a guess is what makes a UI
// promise something the database then refuses.
func TestRowDependentRulesAreNotGuessed(t *testing.T) {
	for _, r := range []string{
		`{"type":"owner","field":"author_id"}`,
		`{"type":"in","column":"site_id","source":{"kind":"rows","table":"t","column":"c"}}`,
		`{"type":"custom","expression":"published"}`,
		`{"type":"compare","op":"eq","left":{"kind":"column","name":"a"},"right":{"kind":"authUid"}}`,
		`{"type":"nullCheck","operand":{"kind":"column","name":"deleted_at"},"isNull":true}`,
	} {
		if got := verdict(t, r, admin); got != VerdictRow {
			t.Errorf("%s: got %q, want row", r, got)
		}
	}
}

// An unreadable or unrecognised rule is not an excuse to assume access.
func TestUnknownRulesFailToRowDependent(t *testing.T) {
	if got := verdict(t, `{"type":"telepathy"}`, admin); got != VerdictRow {
		t.Fatalf("unknown rule: got %q, want row", got)
	}
	if got := Evaluate(json.RawMessage(`not json`), admin); got != VerdictRow {
		t.Fatalf("malformed rule: got %q, want row", got)
	}
}

// One decisive branch settles a composition even when a sibling needs a row: an
// `Any` containing a role the caller has is allowed outright. Reporting "it
// depends" here would make Studio ask per row for something that plainly does not.
func TestCompositionShortCircuits(t *testing.T) {
	anyAdminOrOwner := `{"type":"any","rules":[
		{"type":"role","roles":["admin"]},
		{"type":"owner","field":"author_id"}]}`

	if got := verdict(t, anyAdminOrOwner, admin); got != VerdictAllow {
		t.Errorf("admin should be allowed outright, got %q", got)
	}
	if got := verdict(t, anyAdminOrOwner, reader); got != VerdictRow {
		t.Errorf("a non-admin depends on the row, got %q", got)
	}

	allAdminAndOwner := `{"type":"all","rules":[
		{"type":"role","roles":["admin"]},
		{"type":"owner","field":"author_id"}]}`

	// A failing AND branch settles it: no row can rescue it.
	if got := verdict(t, allAdminAndOwner, reader); got != VerdictDeny {
		t.Errorf("a failed AND branch should deny outright, got %q", got)
	}
	if got := verdict(t, allAdminAndOwner, admin); got != VerdictRow {
		t.Errorf("admin still depends on the row, got %q", got)
	}
}

func TestNotInverts(t *testing.T) {
	banned := Caller{UserID: "u3", AppRole: "banned"}
	rule := `{"type":"not","rule":{"type":"role","roles":["banned"]}}`

	if got := verdict(t, rule, admin); got != VerdictAllow {
		t.Errorf("got %q, want allow", got)
	}
	if got := verdict(t, rule, banned); got != VerdictDeny {
		t.Errorf("got %q, want deny", got)
	}
	// Negating something row-dependent stays row-dependent.
	if got := verdict(t, `{"type":"not","rule":{"type":"owner","field":"a"}}`, admin); got != VerdictRow {
		t.Errorf("got %q, want row", got)
	}
}

// A comparison between two things the session already knows is decidable without
// a row, which is what makes claim-gated tables resolvable up front.
func TestClaimComparisonsResolve(t *testing.T) {
	pro := Caller{
		UserID:  "u4",
		AppRole: "authenticated",
		Claims: map[string]any{
			"app_metadata": map[string]any{"tier": "pro", "seats": float64(5)},
		},
	}

	eqPro := `{"type":"compare","op":"eq",
		"left":{"kind":"claim","path":"app_metadata.tier"},
		"right":{"kind":"literal","value":"pro"}}`
	if got := verdict(t, eqPro, pro); got != VerdictAllow {
		t.Errorf("got %q, want allow", got)
	}
	if got := verdict(t, eqPro, reader); got != VerdictDeny {
		t.Errorf("a caller without the claim: got %q, want deny", got)
	}

	// Numbers compare as the text a policy would cast them to, so 5 and "5" agree
	// here exactly as they do in SQL.
	eqSeats := `{"type":"compare","op":"eq",
		"left":{"kind":"claim","path":"app_metadata.seats"},
		"right":{"kind":"literal","value":5}}`
	if got := verdict(t, eqSeats, pro); got != VerdictAllow {
		t.Errorf("got %q, want allow", got)
	}

	// Ordering is left row-dependent rather than type-guessed.
	gtSeats := `{"type":"compare","op":"gt",
		"left":{"kind":"claim","path":"app_metadata.seats"},
		"right":{"kind":"literal","value":1}}`
	if got := verdict(t, gtSeats, pro); got != VerdictRow {
		t.Errorf("got %q, want row", got)
	}
}

// SQL null comparison is never true, and the evaluator must agree or it will
// report access the database refuses.
func TestNullComparisonIsNeverTrue(t *testing.T) {
	rule := `{"type":"compare","op":"eq",
		"left":{"kind":"claim","path":"missing.path"},
		"right":{"kind":"literal","value":"x"}}`
	if got := verdict(t, rule, admin); got != VerdictDeny {
		t.Fatalf("got %q, want deny", got)
	}

	// An unauthenticated caller's uid is null, so comparing it never matches.
	uidRule := `{"type":"compare","op":"eq",
		"left":{"kind":"authUid"},"right":{"kind":"literal","value":"u1"}}`
	if got := verdict(t, uidRule, anon); got != VerdictDeny {
		t.Fatalf("anon uid: got %q, want deny", got)
	}
}

func TestClaimNullCheck(t *testing.T) {
	withTier := Caller{
		UserID: "u5",
		Claims: map[string]any{"app_metadata": map[string]any{"tier": "pro"}},
	}
	notNull := `{"type":"nullCheck","operand":{"kind":"claim","path":"app_metadata.tier"},"isNull":false}`
	if got := verdict(t, notNull, withTier); got != VerdictAllow {
		t.Errorf("got %q, want allow", got)
	}
	if got := verdict(t, notNull, admin); got != VerdictDeny {
		t.Errorf("missing claim: got %q, want deny", got)
	}
}
