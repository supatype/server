package studioauth

import (
	"net/http"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

// An admin or developer who asks for Studio mode asked to act as Studio, not to
// be elevated. On realtime and function invocation, which have no Studio rule
// to apply, they act as themselves: their own token goes upstream, never the
// service role key.
func TestAskingForStudioModeNeverElevatesRealtimeOrFunctions(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodGet, "/realtime/v1/websocket"},
		{http.MethodPost, "/functions/v1/echo"},
	}
	for _, role := range []string{RoleAdmin, "developer"} {
		for _, r := range routes {
			next, rec := proxyAs(t, role, ModeStudio, r.method, r.path)
			if !next.reached || rec.Header().Get(ActingModeHeader) != ModeSelf {
				t.Errorf("%s %s %s: reached %v, mode %q, want self", role, r.method, r.path,
					next.reached, rec.Header().Get(ActingModeHeader))
			}
			if next.auth == "Bearer service-role-key" || !strings.HasPrefix(next.auth, "Bearer ") {
				t.Errorf("%s %s %s: forwarded %q, want the caller's own token", role, r.method, r.path, next.auth)
			}
		}
	}
}

// A caller's token that already carries a reserved claim, look-alikes
// included, does not carry it into the token Studio mints: only the gateway's
// own Studio claim goes upstream.
func TestMintingDropsReservedClaimsTheCallerCarried(t *testing.T) {
	caller := jwt.MapClaims{"sub": "u", "ſupatype_studio": "write", "SUPATYPE_other": "x", "app": "kept"}
	token, err := mintStudioToken(testSecret, caller, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{}
	_, _, _ = jwt.NewParser().ParseUnverified(token, claims)
	if _, ok := claims["ſupatype_studio"]; ok {
		t.Errorf("look-alike claim survived: %v", claims)
	}
	if _, ok := claims["SUPATYPE_other"]; ok {
		t.Errorf("reserved claim survived: %v", claims)
	}
	if claims["app"] != "kept" || claims[StudioClaim] != "read" {
		t.Errorf("claims %v, want the caller's own claims and the gateway's read", claims)
	}
}
