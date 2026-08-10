package studioauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Tests that lie to the server on purpose.
//
// The rest of the suite checks that a well-formed caller gets the right answer. These check the
// opposite direction: that a *malformed or forged* caller gets nothing. That distinction matters
// because every bug found in this area so far has had the same shape — an operation reporting
// success while doing nothing, or an identity being believed because nothing thought to doubt
// it. A happy-path test cannot see either.
//
// The three axes the access-control plan asks for: **tampered**, **absent**, and **stale**.

func tamperSigned(t *testing.T, claims jwt.MapClaims, secret string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("signing test token: %v", err)
	}
	return s
}

func tamperRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/studio/session", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func tamperClaims(sub string) jwt.MapClaims {
	return jwt.MapClaims{
		"sub": sub,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
}

// A config with no StudioRole hook, so these exercise token verification itself rather than
// membership lookup.
func tamperConfig() Config {
	return Config{JWTSecret: testSecret}
}

// ─── Axis 1: tampered ────────────────────────────────────────────────────────

func TestTamperedSignatureIsRefused(t *testing.T) {
	valid := tamperSigned(t, tamperClaims("user-1"), testSecret)

	// Flip a character in the signature segment, keeping the header and payload intact.
	parts := strings.Split(valid, ".")
	if len(parts) != 3 {
		t.Fatalf("expected three JWT segments, got %d", len(parts))
	}
	sig := []byte(parts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	forged := parts[0] + "." + parts[1] + "." + string(sig)

	res := ResolveAccess(tamperRequest(forged), tamperConfig())
	if res.Allowed {
		t.Fatal("a token with a corrupted signature was allowed")
	}
	if res.Sub != "" {
		t.Errorf("a refused token still yielded a subject %q — nothing downstream should see an identity", res.Sub)
	}
}

func TestTokenSignedWithTheWrongSecretIsRefused(t *testing.T) {
	// The exact attack the published default JWT secret enabled: mint your own token.
	forged := tamperSigned(t, tamperClaims("attacker"), "a-different-secret-entirely-32-chars")

	res := ResolveAccess(tamperRequest(forged), tamperConfig())
	if res.Allowed {
		t.Fatal("a token signed with an unrelated secret was allowed")
	}
	if res.Sub != "" {
		t.Errorf("subject %q leaked from an unverifiable token", res.Sub)
	}
}

func TestPayloadRewrittenWithoutResigningIsRefused(t *testing.T) {
	// Take a legitimately signed token and swap its payload for a privileged one, leaving the
	// original signature. Anything that decodes claims before verifying would be fooled.
	legit := tamperSigned(t, tamperClaims("ordinary-user"), testSecret)
	privileged := tamperSigned(t, jwt.MapClaims{
		"sub":  "ordinary-user",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"role": "service_role",
	}, testSecret)

	legitParts := strings.Split(legit, ".")
	privParts := strings.Split(privileged, ".")
	spliced := legitParts[0] + "." + privParts[1] + "." + legitParts[2]

	res := ResolveAccess(tamperRequest(spliced), tamperConfig())
	if res.Allowed {
		t.Fatal("a token whose payload was replaced under its original signature was allowed")
	}
}

func TestUnsignedAlgNoneTokenIsRefused(t *testing.T) {
	// `alg: none` is the canonical JWT downgrade. Refusing it must not depend on the secret.
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub":  "attacker",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"role": "service_role",
	})
	unsigned, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("building alg=none token: %v", err)
	}

	res := ResolveAccess(tamperRequest(unsigned), tamperConfig())
	if res.Allowed {
		t.Fatal("an alg=none token was allowed")
	}
}

// ─── Axis 2: absent ──────────────────────────────────────────────────────────

func TestAbsentTokenIsUnauthenticatedNotForbidden(t *testing.T) {
	res := ResolveAccess(tamperRequest(""), tamperConfig())
	if res.Allowed {
		t.Fatal("a request with no Authorization header was allowed")
	}
	// The distinction drives the status code: 401 tells a client to authenticate, 403 tells it
	// not to bother. Getting this backwards makes a signed-out Studio look broken.
	if res.Message != "Authentication required" {
		t.Errorf("expected the unauthenticated message so the caller gets 401, got %q", res.Message)
	}
}

func TestGarbageInsteadOfAJWTIsRefused(t *testing.T) {
	for _, token := range []string{
		"not-a-jwt",
		"..",
		"a.b.c",
		strings.Repeat("A", 4096),
	} {
		res := ResolveAccess(tamperRequest(token), tamperConfig())
		if res.Allowed {
			t.Errorf("malformed token %q was allowed", tamperTruncate(token))
		}
	}
}

// ─── Axis 3: stale ───────────────────────────────────────────────────────────

func TestExpiredTokenIsRefused(t *testing.T) {
	claims := jwt.MapClaims{
		"sub": "user-1",
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
		"exp": time.Now().Add(-time.Hour).Unix(), // expired an hour ago
	}
	res := ResolveAccess(tamperRequest(tamperSigned(t, claims, testSecret)), tamperConfig())
	if res.Allowed {
		t.Fatal("an expired token was allowed")
	}
	if res.Sub != "" {
		t.Errorf("expired token still produced subject %q", res.Sub)
	}
}

func TestTokenNotYetValidIsRefused(t *testing.T) {
	claims := jwt.MapClaims{
		"sub": "user-1",
		"nbf": time.Now().Add(time.Hour).Unix(),
		"exp": time.Now().Add(2 * time.Hour).Unix(),
	}
	res := ResolveAccess(tamperRequest(tamperSigned(t, claims, testSecret)), tamperConfig())
	if res.Allowed {
		t.Fatal("a token with a future nbf was allowed")
	}
}

// ─── The privilege-escalation vector that matters most ───────────────────────

// `user_metadata` is writable by the user through GoTrue's own update endpoint. `app_metadata`
// is not. So the application role must never be read from user_metadata — otherwise any signed-in
// user can promote themselves by editing their own profile.
func TestUserMetadataRoleIsIgnored(t *testing.T) {
	claims := tamperClaims("ordinary-user")
	claims["user_metadata"] = map[string]any{"role": "admin"}

	caller := callerFromRequest(
		tamperRequest(tamperSigned(t, claims, testSecret)),
		Result{Allowed: true, Sub: "ordinary-user"},
	)

	if caller.AppRole == "admin" {
		t.Fatal("a role in user_metadata was believed — a user can edit that field, so this is self-promotion")
	}
	if caller.AppRole != "anon" {
		t.Errorf("expected the default app role when only user_metadata names one, got %q", caller.AppRole)
	}
}

func TestAppMetadataRoleIsHonouredAndWinsOverTopLevel(t *testing.T) {
	// Positive control for the test above: the mechanism works, it is only user_metadata that
	// is refused. Without this, "role is ignored" could pass by reading nothing at all.
	claims := tamperClaims("editor-user")
	claims["app_metadata"] = map[string]any{"role": "editor"}
	claims["role"] = "authenticated"

	caller := callerFromRequest(
		tamperRequest(tamperSigned(t, claims, testSecret)),
		Result{Allowed: true, Sub: "editor-user"},
	)

	if caller.AppRole != "editor" {
		t.Errorf("expected app_metadata.role to win, got %q", caller.AppRole)
	}
}

func TestUserMetadataCannotOverrideAppMetadata(t *testing.T) {
	claims := tamperClaims("ordinary-user")
	claims["app_metadata"] = map[string]any{"role": "viewer"}
	claims["user_metadata"] = map[string]any{"role": "admin"}

	caller := callerFromRequest(
		tamperRequest(tamperSigned(t, claims, testSecret)),
		Result{Allowed: true, Sub: "ordinary-user"},
	)

	if caller.AppRole != "viewer" {
		t.Errorf("user_metadata overrode app_metadata: app role became %q", caller.AppRole)
	}
}

// ─── The open-Studio bypass ──────────────────────────────────────────────────

// DevBypass answers unauthenticated requests *as service_role*, which is full database access.
// It therefore has to be impossible to enable by accident, and impossible to reach in production.
func TestDevBypassNeedsBothSwitchesAndNeverInProduction(t *testing.T) {
	cases := []struct {
		name string
		mode string
		open string
		want bool
	}{
		{"nothing set", "", "", false},
		{"dev mode alone", "dev", "", false},
		{"open flag alone", "", "true", false},
		{"open flag in production", "production", "true", false},
		{"open flag with no mode", "", "1", false},
		{"both, explicitly", "dev", "true", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SUPATYPE_MODE", tc.mode)
			t.Setenv("STUDIO_OPEN_DEV", tc.open)
			if got := DevBypass(); got != tc.want {
				t.Errorf("DevBypass()=%v, want %v (SUPATYPE_MODE=%q STUDIO_OPEN_DEV=%q)",
					got, tc.want, tc.mode, tc.open)
			}
		})
	}
}

func TestDevBypassIgnoresAmbiguousFlagValues(t *testing.T) {
	// "maybe" is not consent. A flag this dangerous should read only explicit affirmatives.
	for _, v := range []string{"maybe", "0", "false", "no", "off", " ", "yolo"} {
		t.Setenv("SUPATYPE_MODE", "dev")
		t.Setenv("STUDIO_OPEN_DEV", v)
		if DevBypass() {
			t.Errorf("STUDIO_OPEN_DEV=%q enabled the open-Studio bypass", v)
		}
	}
}

func tamperTruncate(s string) string {
	if len(s) <= 32 {
		return s
	}
	return s[:32] + "…"
}

// ─── Unverified claims must stay decoration ──────────────────────────────────

// `callerFromRequest` decodes claims *without* verifying the signature, which is safe only
// because ResolveAccess has already verified the same token. This pins the precondition: the
// claims path must not be reachable with an identity ResolveAccess refused.
func TestRefusedTokenNeverReachesCallerConstruction(t *testing.T) {
	forged := tamperSigned(t, tamperClaims("attacker"), "wrong-secret-but-long-enough-here")

	res := ResolveAccess(tamperRequest(forged), tamperConfig())
	if res.Allowed {
		t.Fatal("precondition broken: a forged token was allowed")
	}

	// The guard is the caller's, so assert the shape it depends on: a refused result carries no
	// subject, which is what makes an accidental callerFromRequest call harmless.
	if res.Sub != "" {
		t.Errorf("a refused result carried subject %q, so a mistaken caller build would inherit an identity", res.Sub)
	}
}

func TestUnverifiedClaimsRejectsAGarbageToken(t *testing.T) {
	if claims := unverifiedClaims("not.a.token"); claims != nil {
		t.Errorf("expected nil claims from an undecodable token, got %v", claims)
	}
	if claims := unverifiedClaims(""); claims != nil {
		t.Errorf("expected nil claims from an empty token, got %v", claims)
	}
}
