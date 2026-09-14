package studioauth

import (
	"strings"
	"testing"
	"time"
)

// What a preview code is made of, and what must never be recoverable from what is stored.
//
// The database half of this (resolving, listing, revoking) needs a real Postgres and is covered by
// the engine's runtime tests and the end-to-end run. These pin the parts that decide whether a
// stolen table is useful to anybody.

func TestAPreviewCodeSplitsIntoAPublicIDAndASecret(t *testing.T) {
	id, secret, code, err := newPreviewCode()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	gotID, gotSecret, ok := splitPreviewCode(code)
	if !ok {
		t.Fatalf("a freshly minted code must split: %q", code)
	}
	if gotID != id || gotSecret != secret {
		t.Fatalf("split gave (%q, %q), want (%q, %q)", gotID, gotSecret, id, secret)
	}
	if !strings.HasPrefix(id, previewIDPrefix) {
		t.Errorf("the id must be recognisable on sight, got %q", id)
	}
	// Short enough to live in a URL somebody will paste into a message. The whole reason for the
	// id-and-secret split rather than handing out the signed token was that the token made a
	// 400-character link.
	if len(code) > 64 {
		t.Errorf("a code is %d characters, which is back to an unusable URL", len(code))
	}
}

func TestEveryPreviewCodeIsDifferent(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id, secret, _, err := newPreviewCode()
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if seen[id] {
			t.Fatalf("id %q was minted twice: ids are primary keys, so a collision is a lost link", id)
		}
		if seen[secret] {
			t.Fatalf("secret repeated, which would mean the generator is not random")
		}
		seen[id], seen[secret] = true, true
	}
}

func TestRubbishDoesNotSplit(t *testing.T) {
	// Each of these reaches the resolve endpoint from the open internet, so none may be mistaken
	// for the shape of a credential and sent to the database as one.
	for _, code := range []string{
		"",
		"   ",
		"pl_noseparator",
		".onlyasecret",
		"pl_",
		"pl_id.",
		"wrongprefix.secret",
		"pl_id.secret.extra",
	} {
		id, secret, ok := splitPreviewCode(code)
		if code == "pl_id.secret.extra" {
			// A dot in the secret is not an error: Cut takes the first, so extra dots stay in the
			// secret and simply fail the hash comparison.
			if !ok {
				t.Errorf("%q should split, leaving the rest as the secret", code)
			}
			continue
		}
		if ok {
			t.Errorf("%q must not be treated as a code, got id=%q secret=%q", code, id, secret)
		}
	}
}

func TestTheStoredHashDoesNotRevealTheSecret(t *testing.T) {
	_, secret, _, err := newPreviewCode()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	hash := hashPreviewSecret(secret)

	if strings.Contains(hash, secret) {
		t.Fatal("the stored hash contains the secret, so the table is a database of secrets")
	}
	if hash == secret {
		t.Fatal("the secret is stored verbatim")
	}
	if hashPreviewSecret(secret) != hash {
		t.Fatal("hashing is not stable, so no link would ever resolve twice")
	}
	if hashPreviewSecret(secret+"x") == hash {
		t.Fatal("different secrets hash the same")
	}
}

// The exchanged token must never outlive the link that bought it.
//
// This is the arithmetic the resolve handler does. It matters because the link TTL is the thing a
// person chose ("15 minutes") and the exchange window is an implementation detail: a link with ten
// seconds left must not buy a minute of reading.
func TestAnExchangeNeverOutlivesTheLink(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name      string
		linkEnds  time.Time
		wantsLink bool
	}{
		{"a long-lived link is capped by the exchange window", now.Add(time.Hour), false},
		{"a nearly dead link caps the exchange", now.Add(5 * time.Second), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expires := time.Now().Add(previewExchangeTTL)
			if tc.linkEnds.Before(expires) {
				expires = tc.linkEnds
			}
			if tc.wantsLink && !expires.Equal(tc.linkEnds) {
				t.Fatalf("expected the link's own expiry to win, got %v", expires)
			}
			if !tc.wantsLink && !expires.Before(tc.linkEnds) {
				t.Fatalf("expected the exchange window to win, got %v", expires)
			}
			if expires.After(tc.linkEnds) {
				t.Fatal("the exchanged token outlives its link")
			}
		})
	}
}
