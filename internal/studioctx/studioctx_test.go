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
