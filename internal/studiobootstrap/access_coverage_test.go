package studiobootstrap

import (
	"encoding/json"
	"math"
	"testing"
)

// What Studio is told before it has a row in hand. The whole point is that an
// honest "it depends" is safe and a guess is not: a wrong allow offers an action
// the database then refuses, and a wrong deny hides one it would have permitted.

// verdictOf evaluates a rule written as JSON.
func verdictOf(t *testing.T, ruleJSON string, caller Caller) Verdict {
	t.Helper()
	return Evaluate(json.RawMessage(ruleJSON), caller)
}

var (
	anonymous = Caller{}
	signedIn  = Caller{UserID: "user-1", AppRole: "authenticated"}
	editor    = Caller{UserID: "user-2", AppRole: "editor"}
)

// ─── The rules that settle without a row ──────────────────────────────────────

func TestRulesDecidableWithoutARow(t *testing.T) {
	for name, tc := range map[string]struct {
		rule   string
		caller Caller
		want   Verdict
	}{
		"public":                     {`{"type":"public"}`, anonymous, VerdictAllow},
		"private":                    {`{"type":"private"}`, signedIn, VerdictDeny},
		"authenticated, signed in":   {`{"type":"authenticated"}`, signedIn, VerdictAllow},
		"authenticated, anonymous":   {`{"type":"authenticated"}`, anonymous, VerdictDeny},
		"authenticated, blank id":    {`{"type":"authenticated"}`, Caller{UserID: "   "}, VerdictDeny},
		"a role the caller has":      {`{"type":"role","roles":["editor","admin"]}`, editor, VerdictAllow},
		"a role the caller does not": {`{"type":"role","roles":["admin"]}`, editor, VerdictDeny},
		"no roles listed":            {`{"type":"role","roles":[]}`, editor, VerdictDeny},
	} {
		if got := verdictOf(t, tc.rule, tc.caller); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// ─── The rules that need a row ────────────────────────────────────────────────

// Anything that reads a column, or that this package cannot interpret, is
// row-dependent. Answering otherwise is the client-side reimplementation the
// package exists to avoid.
func TestRulesThatNeedARow(t *testing.T) {
	for name, ruleJSON := range map[string]string{
		"ownership":                `{"type":"owner","column":"author_id"}`,
		"membership":               `{"type":"in","column":"team_id"}`,
		"raw SQL":                  `{"type":"custom","expression":"my_check(id)"}`,
		"a rule type nobody knows": `{"type":"something_new"}`,
		"a comparison with a column": `{"type":"compare","op":"eq",
			"left":{"kind":"column","name":"author_id"},"right":{"kind":"authUid"}}`,
		"a comparison with no left":  `{"type":"compare","op":"eq","right":{"kind":"authUid"}}`,
		"a comparison with no right": `{"type":"compare","op":"eq","left":{"kind":"authUid"}}`,
		"an ordering comparison": `{"type":"compare","op":"lte",
			"left":{"kind":"literal","value":1},"right":{"kind":"literal","value":2}}`,
		"a null check on a column": `{"type":"nullCheck","isNull":true,
			"operand":{"kind":"column","name":"deleted_at"}}`,
		"a null check with no operand": `{"type":"nullCheck","isNull":true}`,
		"a negation of nothing":        `{"type":"not"}`,
		"a negation of a column rule":  `{"type":"not","rule":{"type":"owner","column":"author_id"}}`,
	} {
		if got := verdictOf(t, ruleJSON, signedIn); got != VerdictRow {
			t.Errorf("%s: %s, want row", name, got)
		}
	}
}

// An AST that cannot be read is not one to interpret generously.
func TestAnUnreadableRule(t *testing.T) {
	if got := verdictOf(t, `{not json`, signedIn); got != VerdictRow {
		t.Errorf("unreadable: %s, want row", got)
	}
	// But no rule at all is a denial, because deny-by-default is the premise.
	if got := Evaluate(nil, signedIn); got != VerdictDeny {
		t.Errorf("absent: %s, want deny", got)
	}
	if got := Evaluate(json.RawMessage(""), signedIn); got != VerdictDeny {
		t.Errorf("empty: %s, want deny", got)
	}
}

// ─── Negation ─────────────────────────────────────────────────────────────────

// Negating a decidable rule is still decidable, and negating "it depends" still
// depends.
func TestNegation(t *testing.T) {
	for name, tc := range map[string]struct {
		rule string
		want Verdict
	}{
		"not public":  {`{"type":"not","rule":{"type":"public"}}`, VerdictDeny},
		"not private": {`{"type":"not","rule":{"type":"private"}}`, VerdictAllow},
		"not owned":   {`{"type":"not","rule":{"type":"owner","column":"a"}}`, VerdictRow},
	} {
		if got := verdictOf(t, tc.rule, signedIn); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// ─── Any and all ──────────────────────────────────────────────────────────────

// One decisive branch settles the whole thing. An Any containing a role the
// caller has is allowed even beside a row-dependent sibling; getting that wrong
// in the cautious direction reports "it depends" for rules that plainly do not.
func TestAnyAndAll(t *testing.T) {
	owner := `{"type":"owner","column":"author_id"}`
	isEditor := `{"type":"role","roles":["editor"]}`
	isAdmin := `{"type":"role","roles":["admin"]}`

	for name, tc := range map[string]struct {
		rule string
		want Verdict
	}{
		"any: one branch allows":      {`{"type":"any","rules":[` + isEditor + `,` + owner + `]}`, VerdictAllow},
		"any: none allow, one is row": {`{"type":"any","rules":[` + isAdmin + `,` + owner + `]}`, VerdictRow},
		"any: none allow at all":      {`{"type":"any","rules":[` + isAdmin + `,{"type":"private"}]}`, VerdictDeny},
		"any: nothing listed":         {`{"type":"any","rules":[]}`, VerdictDeny},

		"all: one branch denies":     {`{"type":"all","rules":[` + isAdmin + `,` + owner + `]}`, VerdictDeny},
		"all: none deny, one is row": {`{"type":"all","rules":[` + isEditor + `,` + owner + `]}`, VerdictRow},
		"all: every branch allows":   {`{"type":"all","rules":[` + isEditor + `,{"type":"public"}]}`, VerdictAllow},
		"all: nothing listed":        {`{"type":"all","rules":[]}`, VerdictAllow},
	} {
		if got := verdictOf(t, tc.rule, editor); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// ─── Comparisons ──────────────────────────────────────────────────────────────

// A comparison between two things the token supplies is settled here, so Studio
// is not left asking per row about something the caller's own claims decide.
func TestComparisonsBetweenClaims(t *testing.T) {
	caller := Caller{
		UserID:  "user-1",
		AppRole: "editor",
		Claims: map[string]any{
			"tenant": "acme",
			"plan":   map[string]any{"tier": "pro", "seats": float64(5)},
			"active": true,
		},
	}

	compare := func(op, left, right string) string {
		return `{"type":"compare","op":"` + op + `","left":` + left + `,"right":` + right + `}`
	}
	literal := func(v string) string { return `{"kind":"literal","value":` + v + `}` }
	claim := func(path string) string { return `{"kind":"claim","path":"` + path + `"}` }

	for name, tc := range map[string]struct {
		rule string
		want Verdict
	}{
		"a claim equal to a literal":   {compare("eq", claim("tenant"), literal(`"acme"`)), VerdictAllow},
		"a claim unequal to a literal": {compare("eq", claim("tenant"), literal(`"other"`)), VerdictDeny},
		"an inequality that holds":     {compare("neq", claim("tenant"), literal(`"other"`)), VerdictAllow},
		"an inequality that does not":  {compare("neq", claim("tenant"), literal(`"acme"`)), VerdictDeny},
		"a nested claim":               {compare("eq", claim("plan.tier"), literal(`"pro"`)), VerdictAllow},
		"the caller's id":              {compare("eq", `{"kind":"authUid"}`, literal(`"user-1"`)), VerdictAllow},
		"the caller's role":            {compare("eq", `{"kind":"authRole"}`, literal(`"editor"`)), VerdictAllow},
		"a claim that is not there":    {compare("eq", claim("absent"), literal(`"x"`)), VerdictDeny},
		"a path through a non-object":  {compare("eq", claim("tenant.deeper"), literal(`"x"`)), VerdictDeny},
		"a path with no segments":      {compare("eq", claim(""), literal(`"x"`)), VerdictDeny},
		"an anonymous caller's id":     {compare("eq", `{"kind":"authUid"}`, literal(`"user-1"`)), VerdictAllow},
		"an operand kind nobody knows": {compare("eq", `{"kind":"mystery"}`, literal(`"x"`)), VerdictRow},
	} {
		if got := verdictOf(t, tc.rule, caller); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// An anonymous caller has no id, and comparing against one is never true — the
// same as SQL comparing against null.
func TestAnAnonymousCallersIdenticalIsNeverEqual(t *testing.T) {
	rule := `{"type":"compare","op":"eq","left":{"kind":"authUid"},"right":{"kind":"literal","value":"user-1"}}`
	if got := verdictOf(t, rule, anonymous); got != VerdictDeny {
		t.Errorf("%s, want deny", got)
	}
	// And "not equal" against a null is not true either, which is what SQL does.
	notEqual := `{"type":"compare","op":"neq","left":{"kind":"authUid"},"right":{"kind":"literal","value":"user-1"}}`
	if got := verdictOf(t, notEqual, anonymous); got != VerdictDeny {
		t.Errorf("neq: %s, want deny", got)
	}
}

// Values are compared the way Postgres would after the text cast a policy
// applies, so a numeric claim and its string form are the same value here and
// there.
func TestValuesCompareAsTheDatabaseWould(t *testing.T) {
	caller := Caller{
		UserID: "user-1",
		Claims: map[string]any{
			"seats":    float64(5),
			"fraction": 2.5,
			"active":   true,
			"inactive": false,
			"list":     []any{"a", "b"},
		},
	}

	compare := func(path, value string) string {
		return `{"type":"compare","op":"eq","left":{"kind":"claim","path":"` + path +
			`"},"right":{"kind":"literal","value":` + value + `}}`
	}

	for name, tc := range map[string]struct {
		rule string
		want Verdict
	}{
		"a whole number against its digits": {compare("seats", `"5"`), VerdictAllow},
		"a whole number against a number":   {compare("seats", `5`), VerdictAllow},
		"a whole number is not 5.5":         {compare("seats", `5.5`), VerdictDeny},
		"a fraction against its text":       {compare("fraction", `"2.5"`), VerdictAllow},
		"true against its word":             {compare("active", `"true"`), VerdictAllow},
		"false against its word":            {compare("inactive", `"false"`), VerdictAllow},
		"false is not true":                 {compare("inactive", `"true"`), VerdictDeny},
		"a list against its JSON":           {compare("list", `"[\"a\",\"b\"]"`), VerdictAllow},
	} {
		if got := verdictOf(t, tc.rule, caller); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// ─── Null checks ──────────────────────────────────────────────────────────────

// A null check on something the token supplies is decidable; on a column it is
// not.
func TestNullChecks(t *testing.T) {
	caller := Caller{UserID: "user-1", Claims: map[string]any{"tenant": "acme"}}

	check := func(operand string, isNull bool) string {
		want := "false"
		if isNull {
			want = "true"
		}
		return `{"type":"nullCheck","isNull":` + want + `,"operand":` + operand + `}`
	}

	for name, tc := range map[string]struct {
		rule string
		want Verdict
	}{
		"a present claim is not null":   {check(`{"kind":"claim","path":"tenant"}`, false), VerdictAllow},
		"a present claim asked if null": {check(`{"kind":"claim","path":"tenant"}`, true), VerdictDeny},
		"an absent claim is null":       {check(`{"kind":"claim","path":"absent"}`, true), VerdictAllow},
		"an absent claim asked if set":  {check(`{"kind":"claim","path":"absent"}`, false), VerdictDeny},
		"an operand kind nobody knows":  {check(`{"kind":"mystery"}`, true), VerdictRow},
	} {
		if got := verdictOf(t, tc.rule, caller); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// A claim value Go can hold but JSON cannot express renders as nothing rather
// than panicking. Claims is a map[string]any, so nothing stops one arriving.
func TestAValueThatCannotBeRendered(t *testing.T) {
	rule := `{"type":"compare","op":"eq","left":{"kind":"claim","path":"odd"},` +
		`"right":{"kind":"literal","value":"x"}}`

	for name, value := range map[string]any{
		"a function":        func() {},
		"not a number":      math.NaN(),
		"positive infinity": math.Inf(1),
	} {
		caller := Caller{UserID: "user-1", Claims: map[string]any{"odd": value}}
		if got := verdictOf(t, rule, caller); got != VerdictDeny {
			t.Errorf("%s: %s, want deny", name, got)
		}
	}
}

// A comparison with one side missing needs a row, and asking whether that
// missing side varies by caller must not dereference it.
func TestIdentityDependenceOfAComparisonWithASideMissing(t *testing.T) {
	for name, ruleJSON := range map[string]string{
		"no left":  `{"type":"compare","op":"eq","right":{"kind":"literal","value":1}}`,
		"no right": `{"type":"compare","op":"eq","left":{"kind":"literal","value":1}}`,
		"neither":  `{"type":"compare","op":"eq"}`,
	} {
		if IsIdentityDependent(json.RawMessage(ruleJSON)) {
			t.Errorf("%s: a comparison of literals does not vary by caller", name)
		}
	}
}

// A field rule that will not parse is not a public one, so the column stays in
// the masked-field header. Reading it as public would tell a client a null is
// certainly real when nobody can say.
func TestAFieldRuleThatWillNotParseIsNotPublic(t *testing.T) {
	tables, err := maskedFieldsFromAST([]byte(`{"models":[
		{"name":"Post","annotations":{"db":{"tableName":"posts"},
		 "platform":{"access":{"fields":{"broken":{"read":"not-a-rule-object"}}}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	masks := tables["posts"]
	if len(masks) != 1 || masks[0].Column != "broken" {
		t.Errorf("masks = %+v, want the unreadable rule kept", masks)
	}
}

// A model with no table name is keyed by its own name in the field verdicts
// too, or Studio looks up a table it will not find.
func TestFieldVerdictsForAModelWithNoTableName(t *testing.T) {
	snapshot := &Snapshot{AST: json.RawMessage(`{"models":[
		{"name":"Widget","annotations":{"platform":{"access":{"fields":{
			"secret":{"read":{"type":"private"}}}}}}}]}`)}

	verdicts, err := FieldVerdictsForCaller(snapshot, Caller{UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := verdicts["Widget"]; !present {
		t.Errorf("verdicts = %+v, want it keyed by the model name", verdicts)
	}
}
