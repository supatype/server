package modelhooks

import (
	"strings"
	"testing"
)

func TestARequestPathMarkVerifiesAndNamesItsTable(t *testing.T) {
	marks := NewRequestPathMarks("jwt-secret")
	token := marks.Mint("posts")
	table, ok := NewRequestPathMarks("jwt-secret").Verify(token)
	if !ok || table != "posts" {
		t.Fatalf("another replica with the same secret must accept it: %q %v", table, ok)
	}
	if marks.Mint("posts") == token {
		t.Fatal("each mark carries its own nonce")
	}
}

func TestARequestPathMarkCannotBeForged(t *testing.T) {
	token := NewRequestPathMarks("jwt-secret").Mint("posts")
	if _, ok := NewRequestPathMarks("other-secret").Verify(token); ok {
		t.Fatal("a mark from another secret must not verify")
	}
	parts := strings.Split(token, ".")
	retargeted := "b3RoZXI." + parts[1] + "." + parts[2] // base64url("other")
	if _, ok := NewRequestPathMarks("jwt-secret").Verify(retargeted); ok {
		t.Fatal("a mark moved to another table must not verify")
	}
	for _, junk := range []string{"", "x", "a.b", "a.b.c.d", "!!.!!.!!"} {
		if _, ok := NewRequestPathMarks("jwt-secret").Verify(junk); ok {
			t.Fatalf("junk %q verified", junk)
		}
	}
}

func TestNoSecretMeansNoMarks(t *testing.T) {
	marks := NewRequestPathMarks("")
	if marks.Mint("posts") != "" {
		t.Fatal("without a secret nothing can verify a mark, so none is minted")
	}
	if _, ok := marks.Verify("anything"); ok {
		t.Fatal("without a secret nothing verifies")
	}
}
