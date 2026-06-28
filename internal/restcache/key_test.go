package restcache

import (
	"net/http"
	"testing"
)

func TestParseClientMaxAge(t *testing.T) {
	h := http.Header{}
	h.Set("X-Supatype-Cache", "max-age=120")
	if got := ParseClientMaxAge(h); got != 120 {
		t.Fatalf("got %d want 120", got)
	}
	h.Set("X-Supatype-Cache", "max-age=60, public")
	if got := ParseClientMaxAge(h); got != 60 {
		t.Fatalf("got %d want 60", got)
	}
	if ParseClientMaxAge(http.Header{}) != 0 {
		t.Fatal("empty header should be 0")
	}
}

func TestParseClientPublic(t *testing.T) {
	h := http.Header{}
	h.Set("X-Supatype-Cache", "max-age=10, public")
	if !ParseClientPublic(h) {
		t.Fatal("expected public")
	}
	h.Set("X-Supatype-Cache", "max-age=10")
	if ParseClientPublic(h) {
		t.Fatal("expected not public")
	}
}

func TestEffectiveTTL(t *testing.T) {
	if EffectiveTTL(300, 60, true) != 60 {
		t.Fatal("expected min")
	}
	if EffectiveTTL(300, 60, false) != 0 {
		t.Fatal("table not allowed")
	}
	if EffectiveTTL(0, 60, true) != 0 {
		t.Fatal("zero cap")
	}
}

func TestIdentityStableAcrossTokenRefresh(t *testing.T) {
	// Two JWT-shaped tokens with same sub/role but different signatures (not verified here).
	payloadA := "eyJzdWIiOiJ1c2VyLTEiLCJyb2xlIjoiYXV0aGVudGljYXRlZCIsImV4cCI6OTk5OTk5OTk5OX0"
	payloadB := payloadA
	tokenA := "aaa." + payloadA + ".sig"
	tokenB := "bbb." + payloadB + ".other"
	reqA := &http.Request{Header: http.Header{"Authorization": []string{"Bearer " + tokenA}}}
	reqB := &http.Request{Header: http.Header{"Authorization": []string{"Bearer " + tokenB}}}
	idA := IdentityForCache(reqA, nil, false)
	idB := IdentityForCache(reqB, nil, false)
	if idA != idB {
		t.Fatalf("expected stable identity, got %q vs %q", idA, idB)
	}
}

func TestIdentityPublicGlobal(t *testing.T) {
	id := IdentityForCache(&http.Request{Header: http.Header{"Authorization": []string{"Bearer x"}}}, nil, true)
	if id != "global" {
		t.Fatalf("got %q", id)
	}
}

func TestRestTableFromPath(t *testing.T) {
	if RestTableFromPath("/posts") != "posts" {
		t.Fatal("posts")
	}
	if RestTableFromPath("/rpc/foo") != "" {
		t.Fatal("rpc skipped")
	}
}

func TestBuildKeyStable(t *testing.T) {
	p := keyParts{Tenant: "demo", Schema: "public", Method: "GET", Path: "/posts", AuthHash: "abc"}
	k1 := BuildKey(p)
	k2 := BuildKey(p)
	if k1 != k2 {
		t.Fatalf("unstable keys %q %q", k1, k2)
	}
	if k1[:len("tenant:demo:rest:")] != "tenant:demo:rest:" {
		t.Fatalf("bad prefix %q", k1)
	}
}
