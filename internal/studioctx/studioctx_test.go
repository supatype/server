package studioctx

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func unsigned(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"HS256"}`)) + "." + enc([]byte(payload)) + ".sig"
}

func TestReservedClaimNamesFoldCase(t *testing.T) {
	for name, want := range map[string]bool{
		"supatype_studio": true, "SUPATYPE_studio": true, "ſupatype_studio": true,
		"supatype_": true, "supatype": false, "sub": false, "app_supatype_x": false, "": false,
	} {
		if got := IsReservedClaim(name); got != want {
			t.Errorf("IsReservedClaim(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestTokensCarryingAReservedClaim(t *testing.T) {
	for payload, want := range map[string]bool{
		`{"sub":"u","supatype_studio":"read"}`:                true,
		`{"sub":"u","supatype` + `\` + `u005fstudio":"read"}`: true,
		`{"sub":"u","app_metadata":{"supatype_x":1}}`:         false,
		`{"sub":"u"}`: false,
		`not json`:    false,
	} {
		if got := TokenCarriesReservedClaim(unsigned(payload)); got != want {
			t.Errorf("%s: got %v, want %v", payload, got, want)
		}
	}
}

func TestOnlyTheProxyMayPresentTheClaim(t *testing.T) {
	reached := false
	h := RefuseForgedClaim(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	token := unsigned(`{"sub":"u","supatype_studio":"write"}`)

	req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || reached {
		t.Fatalf("public: status %d, reached %v", rec.Code, reached)
	}

	req = req.WithContext(WithProxied(context.Background()))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !reached {
		t.Fatal("the Studio proxy's own request was refused")
	}
}

// A payload that is not base64url is not a token carrying anything, and a
// value with the wrong number of segments is not a JWT at all.
func TestValuesThatAreNotTokens(t *testing.T) {
	for _, value := range []string{"a.!!!.c", "a.b", "", "a.b.c.d"} {
		if TokenCarriesReservedClaim(value) {
			t.Errorf("%q: read as carrying a reserved claim", value)
		}
	}
}

// Every place a token travels is checked: the apikey header and each query
// value as well as the bearer, and a request that carries no reserved claim
// anywhere goes through.
func TestTheClaimIsRefusedWhereverTheTokenTravels(t *testing.T) {
	forged := unsigned(`{"sub":"u","supatype_studio":"write"}`)
	plain := unsigned(`{"sub":"u","role":"authenticated"}`)
	for _, tc := range []struct {
		name, apikey, bearer, query string
		refused                     bool
	}{
		{"apikey header", forged, plain, "", true},
		{"realtime query token", plain, plain, "?apikey=x&token=" + forged, true},
		{"signed link query token", "", "", "?token=" + forged, true},
		{"no reserved claim anywhere", plain, plain, "?token=" + plain + "&select=*", false},
		{"no token at all", "", "", "", false},
	} {
		reached := false
		h := RefuseForgedClaim(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
		req := httptest.NewRequest(http.MethodGet, "/rest/v1/posts"+tc.query, nil)
		if tc.apikey != "" {
			req.Header.Set("apikey", tc.apikey)
		}
		if tc.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if refused := rec.Code == http.StatusUnauthorized; refused != tc.refused || reached == tc.refused {
			t.Errorf("%s: status %d, reached %v, want refused %v", tc.name, rec.Code, reached, tc.refused)
		}
	}
}
