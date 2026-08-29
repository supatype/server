package mau

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A refresh is deliberately not counted: a background token rotation is not a
// person arriving, and counting it would bill the customer for their own
// clients' timers.
func TestEligible(t *testing.T) {
	cases := []struct {
		method, path, grant string
		want                bool
	}{
		{http.MethodPost, "/auth/v1/signup", "", true},
		{http.MethodPost, "/auth/v1/verify", "", true},
		{http.MethodPost, "/auth/v1/token", "password", true},
		{http.MethodPost, "/auth/v1/token", "id_token", true},
		{http.MethodPost, "/auth/v1/token", "pkce", true},

		{http.MethodPost, "/auth/v1/token", "refresh_token", false},
		{http.MethodPost, "/auth/v1/token", "", false},
		{http.MethodGet, "/auth/v1/signup", "", false},
		{http.MethodPost, "/auth/v1/logout", "", false},
		{http.MethodPost, "/rest/v1/things", "", false},
		{http.MethodPost, "/auth/v1/signup/extra", "", false},
	}
	for _, c := range cases {
		if got := Eligible(c.method, c.path, c.grant); got != c.want {
			t.Errorf("Eligible(%s %s grant=%q) = %v, want %v", c.method, c.path, c.grant, got, c.want)
		}
	}
}

// Staging and preview bill to the same organisation as the project, so they must
// resolve to the same ref and share a day's set.
func TestProjectRef(t *testing.T) {
	for tenant, want := range map[string]string{
		"abcdef":             "abcdef",
		"abcdef-staging":     "abcdef",
		"abcdef-preview":     "abcdef",
		"abcdef-production":  "abcdef-production",
		"my-project":         "my-project",
		"my-project-staging": "my-project",
		"-staging":           "-staging",
		"":                   "",
	} {
		if got := ProjectRef(tenant); got != want {
			t.Errorf("ProjectRef(%q) = %q, want %q", tenant, got, want)
		}
	}
}

// A federated identity is the same person across projects and devices, so it
// must win over anything project-scoped.
func TestDedupeKeyPrefersAFederatedIdentity(t *testing.T) {
	user := map[string]any{
		"id":    "local-id",
		"email": "someone@example.com",
		"identities": []any{
			map[string]any{"provider": "google", "identity_id": "sub-123"},
		},
	}
	if got := DedupeKey("salt", "proj", user); got != "oidc:google:sub-123" {
		t.Errorf("got %q", got)
	}
}

func TestDedupeKeyFallsBackThroughTheIdentityList(t *testing.T) {
	user := map[string]any{
		"identities": []any{
			"not a map",
			map[string]any{"provider": "", "identity_id": "no-provider"},
			map[string]any{"provider": "github", "id": "legacy-id-field"},
		},
	}
	if got := DedupeKey("salt", "proj", user); got != "oidc:github:legacy-id-field" {
		t.Errorf("got %q", got)
	}
}

func TestDedupeKeyHashesTheEmail(t *testing.T) {
	user := map[string]any{"email": "  Someone@Example.COM  ", "id": "local-id"}
	got := DedupeKey("pepper", "proj", user)

	if !strings.HasPrefix(got, "email:") {
		t.Fatalf("got %q, want an email key", got)
	}
	// The address must not be recoverable from the tally.
	if strings.Contains(got, "example.com") || strings.Contains(got, "Someone") {
		t.Errorf("the key leaks the address: %q", got)
	}
	// Case and surrounding space must not produce a second person.
	if other := DedupeKey("pepper", "proj", map[string]any{"email": "someone@example.com"}); other != got {
		t.Errorf("normalisation failed: %q vs %q", got, other)
	}
	// A different salt must produce a different key, or the salt is decorative.
	if same := DedupeKey("other-pepper", "proj", user); same == got {
		t.Error("the salt does not affect the key")
	}
}

// Without a salt, an email hash could be confirmed against a guessed address by
// anyone holding the tally, so the email form is skipped entirely.
func TestDedupeKeyWithoutASaltDoesNotHashTheEmail(t *testing.T) {
	user := map[string]any{"email": "someone@example.com", "id": "local-id"}
	got := DedupeKey("", "proj", user)
	if got != "local:proj:local-id" {
		t.Errorf("got %q, want the project-scoped local id", got)
	}
}

// The local id is scoped to the project: the same person signing up to two
// projects has two ids and must not be silently merged into one billed user.
func TestDedupeKeyLocalIsProjectScoped(t *testing.T) {
	user := map[string]any{"id": "user-1"}
	if a, b := DedupeKey("s", "proj-a", user), DedupeKey("s", "proj-b", user); a == b {
		t.Error("the same local id in two projects must not collide")
	}
	if got := DedupeKey("s", "proj", map[string]any{}); got != "local:proj:unknown" {
		t.Errorf("a user with no id at all: got %q", got)
	}
}

func TestDayKey(t *testing.T) {
	day := time.Date(2026, 8, 28, 23, 30, 0, 0, time.UTC)
	if got := DayKey("org-1", day); got != "mau:org:org-1:d:2026-08-28" {
		t.Errorf("got %q", got)
	}
	// The day is UTC wherever the process thinks it is, so two pods in different
	// zones agree on which set to write.
	east := time.FixedZone("east", 10*3600)
	if got := DayKey("org-1", day.In(east)); got != "mau:org:org-1:d:2026-08-28" {
		t.Errorf("local zone leaked into the key: %q", got)
	}
}

type recordingStore struct {
	key, member string
	expireAt    time.Time
	err         error
	calls       int
}

func (s *recordingStore) AddToExpiringSet(_ context.Context, key, member string, expireAt time.Time) error {
	s.calls++
	s.key, s.member, s.expireAt = key, member, expireAt
	return s.err
}

func TestRecord(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	store := &recordingStore{}
	r := Recorder{Store: store, Salt: "pepper", Now: func() time.Time { return now }}

	if err := r.Record(context.Background(), "org-1", "proj", map[string]any{"id": "u1"}); err != nil {
		t.Fatal(err)
	}
	if store.key != "mau:org:org-1:d:2026-08-28" {
		t.Errorf("key = %q", store.key)
	}
	if store.member != "local:proj:u1" {
		t.Errorf("member = %q", store.member)
	}
	// Forty days, so a month's rollup can still read the last day after it closes.
	if want := now.Add(retention); !store.expireAt.Equal(want) {
		t.Errorf("expiry = %v, want %v", store.expireAt, want)
	}
}

func TestRecordReportsStoreFailure(t *testing.T) {
	store := &recordingStore{err: errors.New("valkey down")}
	r := Recorder{Store: store}
	if err := r.Record(context.Background(), "org", "proj", map[string]any{"id": "u"}); err == nil {
		t.Fatal("want the store failure reported to the caller")
	}
}

func TestRecordUsesTheRealClockByDefault(t *testing.T) {
	store := &recordingStore{}
	if err := (Recorder{Store: store}).Record(context.Background(), "org", "proj", map[string]any{"id": "u"}); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 {
		t.Fatalf("calls = %d", store.calls)
	}
	if !strings.HasPrefix(store.key, "mau:org:org:d:") {
		t.Errorf("key = %q", store.key)
	}
}

// An identities field that is present but not a list, and a list whose entries
// never yield a usable provider and subject, must both fall through rather than
// producing a half-formed federated key.
func TestFederatedKeyRejectsUnusableIdentities(t *testing.T) {
	for name, user := range map[string]map[string]any{
		"identities is not a list": {"identities": "google", "id": "u"},
		"identities is a number":   {"identities": 42, "id": "u"},
		"empty list":               {"identities": []any{}, "id": "u"},
		"entries missing a subject": {
			"identities": []any{map[string]any{"provider": "google"}},
			"id":         "u",
		},
		"entries missing a provider": {
			"identities": []any{map[string]any{"identity_id": "sub"}},
			"id":         "u",
		},
	} {
		if got := DedupeKey("", "proj", user); got != "local:proj:u" {
			t.Errorf("%s: got %q, want the local fallback", name, got)
		}
	}
}
