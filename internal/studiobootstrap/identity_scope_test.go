package studiobootstrap

import (
	"encoding/json"
	"testing"
)

func identityDependent(t *testing.T, ruleJSON string) bool {
	t.Helper()
	return IsIdentityDependent(json.RawMessage(ruleJSON))
}

// The question is "does the answer vary by *who* is asking", because that is what
// decides whether one cached response may be served to everyone.
func TestIdentityIndependentRulesMaySharedCache(t *testing.T) {
	for _, r := range []string{
		`{"type":"public"}`,
		`{"type":"private"}`,
		// Varies by row and by time, but every caller gets the same answer for a
		// given row — which is the whole point of allowing shared caching on a
		// published-content table.
		`{"type":"compare","op":"lte","left":{"kind":"column","name":"published_at"},"right":{"kind":"now"}}`,
		`{"type":"compare","op":"gte","left":{"kind":"column","name":"created_at"},"right":{"kind":"ago","amount":30,"unit":"days"}}`,
		`{"type":"nullCheck","operand":{"kind":"column","name":"deleted_at"},"isNull":true}`,
		`{"type":"any","rules":[{"type":"public"},{"type":"nullCheck","operand":{"kind":"column","name":"a"},"isNull":true}]}`,
	} {
		if identityDependent(t, r) {
			t.Errorf("%s should be shareable", r)
		}
	}
}

// Anything whose answer differs per caller must not share one entry — the first
// requester's rows would be served to everyone until the TTL expires.
func TestIdentityDependentRulesMayNotShareCache(t *testing.T) {
	for _, r := range []string{
		`{"type":"owner","field":"author_id"}`,
		`{"type":"role","roles":["admin"]}`,
		// Distinguishes signed-in from anonymous, so a shared entry could serve a
		// member's view to a stranger.
		`{"type":"authenticated"}`,
		`{"type":"in","column":"site_id","source":{"kind":"rows","table":"user_sites","column":"site_id"}}`,
		`{"type":"exists","source":{"kind":"rows","table":"user_sites","column":"site_id"}}`,
		`{"type":"compare","op":"eq","left":{"kind":"column","name":"a"},"right":{"kind":"authUid"}}`,
		`{"type":"compare","op":"eq","left":{"kind":"authRole"},"right":{"kind":"literal","value":"admin"}}`,
		`{"type":"compare","op":"eq","left":{"kind":"column","name":"tier"},"right":{"kind":"claim","path":"app_metadata.tier"}}`,
		`{"type":"nullCheck","operand":{"kind":"claim","path":"app_metadata.tier"},"isNull":false}`,
	} {
		if !identityDependent(t, r) {
			t.Errorf("%s must not be shareable", r)
		}
	}
}

// One identity-dependent branch taints the whole composition: the rule as a whole
// then answers differently per caller.
func TestOneDependentBranchTaintsTheComposition(t *testing.T) {
	mixed := `{"type":"any","rules":[
		{"type":"compare","op":"lte","left":{"kind":"column","name":"published_at"},"right":{"kind":"now"}},
		{"type":"role","roles":["editor"]}]}`
	if !identityDependent(t, mixed) {
		t.Fatal("a rule with a role branch must not be shareable")
	}

	nested := `{"type":"all","rules":[
		{"type":"public"},
		{"type":"not","rule":{"type":"owner","field":"a"}}]}`
	if !identityDependent(t, nested) {
		t.Fatal("a negated owner rule is still per caller")
	}
}

// Guessing "shareable" on a parse failure would start serving one caller's rows to
// another on the strength of a bug.
func TestUnreadableRulesAreNotShareable(t *testing.T) {
	if !IsIdentityDependent(json.RawMessage(`not json`)) {
		t.Error("a malformed rule must not be shareable")
	}
	if !identityDependent(t, `{"type":"telepathy"}`) {
		t.Error("an unrecognised rule must not be shareable")
	}
}

// An absent read rule denies, so there is nothing to cache and nothing to share.
func TestAbsentRuleIsNotIdentityDependent(t *testing.T) {
	if IsIdentityDependent(nil) {
		t.Error("an absent rule denies; it does not vary by caller")
	}
}
