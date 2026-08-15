package studiobootstrap

import (
	"os"
	"testing"
)

// The classification is read out of an AST snapshot written by the schema engine, so the
// struct this package uses has to match what the engine actually emits — not what it is
// assumed to emit. The fixture is a copy of the engine's own
// `tests/fixtures/rls_field_rules.json`, which carries all three field-rule shapes.
//
// If the engine's AST layout changes, this fails here rather than by silently classifying
// nothing and quietly dropping the header.
func TestMaskedFieldsFromEngineAST(t *testing.T) {
	raw, err := os.ReadFile("testdata/engine_field_rules_ast.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	tables, err := maskedFieldsFromAST(raw)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}

	masks, ok := tables["users"]
	if !ok {
		t.Fatalf("no classification for `users` — the AST shape assumption is wrong, got %#v", tables)
	}

	byColumn := make(map[string]FieldMask, len(masks))
	for _, m := range masks {
		byColumn[m.Column] = m
	}

	// `email` has a read rule of `Role<"admin">` — restricted, and the same verdict for
	// every row, so the header can speak for the whole result set.
	email, ok := byColumn["email"]
	if !ok {
		t.Fatal("`email` has a read rule and must be reported")
	}
	if email.RowDependent {
		t.Error("a role-based read rule does not vary by row")
	}

	// `name` has both read and write rules; the read rule is what matters here.
	if _, ok := byColumn["name"]; !ok {
		t.Error("`name` has a read rule and must be reported")
	}

	// `id` is write-only. A write restriction does not hide anything on read, so reporting
	// it would tell a client to expect nulls that will never come.
	if _, ok := byColumn["id"]; ok {
		t.Error("a write-only rule must not be reported as a masked field")
	}

	// Models with no field rules contribute nothing.
	if _, ok := tables["posts"]; ok {
		t.Error("a model with no field rules must not appear")
	}
}

// Order has to be stable so the header value does not churn between requests.
func TestMaskedFieldsAreSorted(t *testing.T) {
	raw := []byte(`{"models":[{"name":"T","annotations":{"db":{"tableName":"t"},
	  "platform":{"access":{"fields":{
	    "zeta":{"read":{"type":"role","roles":["a"]}},
	    "alpha":{"read":{"type":"role","roles":["a"]}},
	    "mid":{"read":{"type":"role","roles":["a"]}}}}}}}]}`)

	tables, err := maskedFieldsFromAST(raw)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	got := []string{}
	for _, m := range tables["t"] {
		got = append(got, m.Column)
	}
	want := []string{"alpha", "mid", "zeta"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("columns not sorted: got %v, want %v", got, want)
		}
	}
}

// A read rule of `public` restricts nothing, so naming it in a masking header is noise.
func TestPublicReadRuleIsNotReported(t *testing.T) {
	raw := []byte(`{"models":[{"name":"T","annotations":{"db":{"tableName":"t"},
	  "platform":{"access":{"fields":{"open":{"read":{"type":"public"}}}}}}}]}`)

	tables, err := maskedFieldsFromAST(raw)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if _, ok := tables["t"]; ok {
		t.Error("a public read rule must not be reported as masked")
	}
}
