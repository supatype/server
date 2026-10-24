package studiomembers

import (
	"context"
	"strings"
	"testing"
)

// An id written another way Postgres accepts is still the same user, so a
// self check cannot be stepped round by writing your own id in capitals.
func TestSameUser(t *testing.T) {
	const id = "aaaaaaaa-0000-4000-8000-000000000001"
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{id, id, true},
		{id, "AAAAAAAA-0000-4000-8000-000000000001", true},
		{id, "{" + id + "}", true},
		{id, "aaaaaaaa000040008000000000000001", true},
		{id, " " + id + " ", true},
		{id, "bbbbbbbb-0000-4000-8000-000000000001", false},
		{"", "", false},
		{id, "", false},
	} {
		if got := SameUser(tc.a, tc.b); got != tc.want {
			t.Errorf("SameUser(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// The store refuses your own id however it is written, before it asks the
// database anything: a store with no database still says why.
func TestTheStoreRefusesYourOwnIdHoweverWritten(t *testing.T) {
	const id = "aaaaaaaa-0000-4000-8000-000000000001"
	ctx := context.Background()
	for _, target := range []string{id, "AAAAAAAA-0000-4000-8000-000000000001", "{" + id + "}"} {
		if err := (Store{}).SetRole(ctx, id, target, "editor"); err == nil || !strings.Contains(err.Error(), "your own") {
			t.Errorf("SetRole on %s: %v", target, err)
		}
		if err := (Store{}).Revoke(ctx, id, target); err == nil || !strings.Contains(err.Error(), "your own") {
			t.Errorf("Revoke on %s: %v", target, err)
		}
	}
}
