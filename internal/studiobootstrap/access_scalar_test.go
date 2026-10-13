package studiobootstrap

import "testing"

// A claim is compared as the text Postgres gives it (jsonb ->>), so a number
// must render exactly: trimming zeros made 10 read as "1" and 70 as "7", and
// Studio then showed an allow the policy refuses.
func TestScalarTextRendersNumbersExactly(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{7, "7"}, {70, "70"}, {10, "10"}, {1, "1"}, {100, "100"},
		{0, "0"}, {1.5, "1.5"}, {-20, "-20"}, {-0.25, "-0.25"},
		{1e21, "1000000000000000000000"},
	} {
		if got := scalarText(tc.in); got != tc.want {
			t.Errorf("scalarText(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSameValueKeepsDistinctNumbersDistinct(t *testing.T) {
	for _, tc := range []struct {
		a, b any
		want bool
	}{
		{float64(7), float64(70), false},
		{float64(10), float64(1), false},
		{float64(100), float64(1), false},
		{float64(10), "10", true},
		{float64(0), "0", true},
		{float64(1.5), "1.5", true},
		{float64(-20), "-20", true},
		{float64(-20), "-2", false},
	} {
		if got := sameValue(tc.a, tc.b); got != tc.want {
			t.Errorf("sameValue(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
