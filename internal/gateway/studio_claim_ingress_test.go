package gateway

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

// signedClaims signs a raw JSON payload with the rig's secret. Raw so a test can
// spell a claim name exactly, look-alike runes included.
func signedClaims(t *testing.T, payload string) string {
	t.Helper()
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	sig, err := jwt.SigningMethodHS256.Sign(head+"."+body, []byte(testJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// The Studio claim admits a request to every row. Only the Studio proxy mints
// it, in process, so the same claim arriving at a public data-plane route is
// forged or leaked and is refused before any upstream sees it, wherever the
// token travels and however its name is spelled.
func TestAPublicRequestCarryingTheStudioClaimIsRefused(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	forged := map[string]string{
		"studio":     `{"sub":"u1","role":"authenticated","supatype_studio":"write","exp":` + itoa(int(exp)) + `}`,
		"look-alike": `{"sub":"u1","role":"authenticated","ſupatype_studio":"write","exp":` + itoa(int(exp)) + `}`,
		"namespace":  `{"sub":"u1","role":"authenticated","SUPATYPE_other":"x","exp":` + itoa(int(exp)) + `}`,
	}
	targets := []string{"/rest/v1/posts", "/graphql/v1", "/realtime/v1/websocket", "/functions/v1/echo", "/storage/v1/object/b/k"}
	for name, payload := range forged {
		token := signedClaims(t, payload)
		for _, target := range targets {
			assertRefused(t, name+" bearer "+target, target, func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer "+token)
			})
			assertRefused(t, name+" query "+target, target+"?apikey="+token, nil)
		}
	}
}

func assertRefused(t *testing.T, name, target string, mutate func(*http.Request)) {
	t.Helper()
	r := newRig(t, "dev")
	rec := r.do(http.MethodPost, target, mutate)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("%s: status %d, want 401", name, rec.Code)
	}
	if got, ok := r.lastHit(); ok {
		t.Errorf("%s: reached %s", name, got.upstream)
	}
}

// An ordinary token still reaches the data plane.
func TestAPublicRequestWithoutTheStudioClaimPasses(t *testing.T) {
	token := signedClaims(t, `{"sub":"u1","role":"authenticated","app_claim":"x"}`)
	r := newRig(t, "dev")
	rec := r.do(http.MethodGet, "/rest/v1/posts", func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+token)
	})
	if got, ok := r.lastHit(); rec.Code != http.StatusOK || !ok || got.upstream != "postgrest" {
		t.Fatalf("status %d, reached %v", rec.Code, ok)
	}
}

// Studio's own minted request, forwarded by the proxy in process, still reaches
// PostgREST carrying the claim.
func TestStudioProxyStillForwardsItsMintedClaim(t *testing.T) {
	admin := signedClaims(t, `{"sub":"admin-user","role":"authenticated","app_metadata":{"role":"admin"},"exp":`+
		itoa(int(time.Now().Add(time.Hour).Unix()))+`}`)
	r := newRig(t, "dev")
	rec := r.do(http.MethodGet, "/studio/proxy/rest/v1/posts", func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+admin)
		req.Header.Set("X-Supatype-Acting-Mode", "studio")
	})
	got, ok := r.lastHit()
	if rec.Code != http.StatusOK || !ok || got.upstream != "postgrest" {
		t.Fatalf("status %d (%s), reached %v", rec.Code, rec.Body.String(), ok)
	}
	minted := strings.TrimPrefix(got.header.Get("Authorization"), "Bearer ")
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(minted, claims); err != nil || claims["supatype_studio"] == nil {
		t.Fatalf("upstream token %v carries no Studio claim (%v)", claims, err)
	}
}
