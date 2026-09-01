package studiobootstrap

import (
	"reflect"
	"testing"
)

// Row-dependence decides whether a masked-field header can speak for a whole result set or
// only warn. Getting it wrong in the permissive direction makes a client hide values the
// caller was entitled to see, so the table below is the contract.
func TestIsRowDependent(t *testing.T) {
	cases := []struct {
		name string
		rule string
		want bool
	}{
		// Varies by caller, not by row — the header can speak for every row.
		{"public", `{"type":"public"}`, false},
		{"private", `{"type":"private"}`, false},
		{"authenticated", `{"type":"authenticated"}`, false},
		{"role", `{"type":"role","roles":["admin"]}`, false},

		// The case that makes this a different question from identity-dependence: `owner`
		// is identity-dependent *and* row-dependent.
		{"owner", `{"type":"owner","field":"author_id"}`, true},

		// Membership and existence correlate a column of this row against another table.
		{"in", `{"type":"in","column":"site_id","source":{}}`, true},
		{"exists", `{"type":"exists","source":{}}`, true},

		// A comparison is row-dependent exactly when an operand reads a column.
		{
			"compare column to now",
			`{"type":"compare","op":"lte","left":{"kind":"column","name":"published_at"},"right":{"kind":"now"}}`,
			true,
		},
		{
			"compare claim to literal",
			`{"type":"compare","op":"eq","left":{"kind":"claim","path":"app_metadata.tier"},"right":{"kind":"literal","value":"pro"}}`,
			false,
		},

		{"nullCheck on a column", `{"type":"nullCheck","operand":{"kind":"column","name":"deleted_at"}}`, true},
		{"nullCheck on authUid", `{"type":"nullCheck","operand":{"kind":"authUid"}}`, false},

		// Composition is row-dependent if any branch is.
		{
			"any with one row-dependent branch",
			`{"type":"any","rules":[{"type":"role","roles":["admin"]},{"type":"owner","field":"author_id"}]}`,
			true,
		},
		{
			"all with no row-dependent branch",
			`{"type":"all","rules":[{"type":"role","roles":["admin"]},{"type":"authenticated"}]}`,
			false,
		},
		{"not wrapping a row-dependent rule", `{"type":"not","rule":{"type":"owner","field":"id"}}`, true},

		// Unknown and unreadable rules are assumed row-dependent: that only costs
		// precision, never correctness in the direction that hides data.
		{"unrecognised type", `{"type":"someFutureRule"}`, true},
		{"unparseable", `not json`, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsRowDependent([]byte(c.rule)); got != c.want {
				t.Fatalf("IsRowDependent(%s) = %v, want %v", c.rule, got, c.want)
			}
		})
	}
}

// An absent rule restricts nothing.
func TestIsRowDependentEmpty(t *testing.T) {
	if IsRowDependent(nil) {
		t.Fatal("an absent rule must not be reported as row-dependent")
	}
}

// Row-dependence and identity-dependence are different axes, and conflating them is the
// mistake this pair of functions exists to prevent.
func TestRowAndIdentityDependenceAreDifferentQuestions(t *testing.T) {
	roleRule := []byte(`{"type":"role","roles":["admin"]}`)
	if !IsIdentityDependent(roleRule) || IsRowDependent(roleRule) {
		t.Fatal("a role rule should be identity-dependent but not row-dependent")
	}

	publishedRule := []byte(`{"type":"compare","op":"lte","left":{"kind":"column","name":"published_at"},"right":{"kind":"now"}}`)
	if IsIdentityDependent(publishedRule) || !IsRowDependent(publishedRule) {
		t.Fatal("a temporal rule should be row-dependent but not identity-dependent")
	}

	ownerRule := []byte(`{"type":"owner","field":"author_id"}`)
	if !IsIdentityDependent(ownerRule) || !IsRowDependent(ownerRule) {
		t.Fatal("an owner rule should be both, which is why the header cannot be exact for it")
	}
}

// The masks in a header come out of a map, so the order they are built in is
// whatever Go's iteration gives that run. Sorting is what makes the header
// stable across requests, and it was only ever exercised when the map happened
// to yield an out-of-order pair: the swap inside sortMasks was covered on some
// runs and not others, which made the coverage gate itself intermittent.
func TestSortMasksOrdersColumns(t *testing.T) {
	masks := []FieldMask{
		{Column: "ssn", RowDependent: true},
		{Column: "email"},
		{Column: "address", RowDependent: true},
		{Column: "dob"},
	}

	sortMasks(masks)

	var columns []string
	for _, m := range masks {
		columns = append(columns, m.Column)
	}
	want := []string{"address", "dob", "email", "ssn"}
	if !reflect.DeepEqual(columns, want) {
		t.Errorf("columns = %v, want %v", columns, want)
	}
	// The rest of the mask travels with its column rather than staying put.
	for _, m := range masks {
		if wantDependent := m.Column == "ssn" || m.Column == "address"; m.RowDependent != wantDependent {
			t.Errorf("%s: RowDependent = %v, want %v", m.Column, m.RowDependent, wantDependent)
		}
	}
}

// Sorting something already sorted, or with nothing to sort, leaves it alone.
func TestSortMasksOnTrivialInput(t *testing.T) {
	sortMasks(nil)

	one := []FieldMask{{Column: "email"}}
	sortMasks(one)
	if len(one) != 1 || one[0].Column != "email" {
		t.Errorf("one mask = %v", one)
	}
}
