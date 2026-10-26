package tokens

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/auth/hooks/v0hooks"
)

func baseHookClaims() map[string]interface{} {
	return map[string]interface{}{
		"aud":          "authenticated",
		"exp":          1700000000,
		"iat":          1600000000,
		"sub":          "00000000-0000-0000-0000-000000000001",
		"email":        "a@example.com",
		"phone":        "",
		"role":         "authenticated",
		"aal":          "aal1",
		"session_id":   "s1",
		"is_anonymous": false,
	}
}

func TestIsReservedClaim(t *testing.T) {
	cases := map[string]bool{
		"supatype_studio":      true,
		"supatype_":            true,
		"SUPATYPE_studio":      true,
		"Supatype_Anything":    true,
		"\u017fupatype_studio": true,
		"supatype":             false,
		"supatypestudio":       false,
		"my_supatype_studio":   false,
		"custom_claim":         false,
		"":                     false,
		"\u017f":               false,
	}
	for name, want := range cases {
		require.Equal(t, want, isReservedClaim(name), "claim %q", name)
	}
}

func TestValidateTokenClaimsRefusesReservedClaims(t *testing.T) {
	for _, key := range []string{"supatype_studio", "\u017fupatype_studio", "SUPATYPE_studio"} {
		claims := baseHookClaims()
		claims[key] = map[string]interface{}{"role": "owner"}
		err := validateTokenClaims(claims)
		require.Error(t, err, "claim %q must be refused", key)
		var httpErr *apierrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		require.Equal(t, http.StatusInternalServerError, httpErr.HTTPStatus)
		require.Contains(t, httpErr.Message, "reserved")
	}
}

func TestValidateTokenClaimsAllowsCustomClaim(t *testing.T) {
	claims := baseHookClaims()
	claims["tenant_id"] = "t1"
	claims["app_metadata"] = map[string]interface{}{"supatype_studio": "nested is not top level"}
	require.NoError(t, validateTokenClaims(claims))
}

// The hook answers in JSON, so a look-alike key can arrive escaped. Decoding
// it the way the hook manager does must still end in a refusal.
func TestHookResponseSettingStudioClaimIsRefused(t *testing.T) {
	for _, key := range []string{`supatype_studio`, `\u017fupatype_studio`, `SUPATYPE_STUDIO`} {
		body := `{"claims":{"aud":"authenticated","exp":1700000000,"iat":1600000000,` +
			`"sub":"u","email":"","phone":"","role":"authenticated","aal":"aal1",` +
			`"session_id":"s","is_anonymous":false,"` + key + `":{"role":"owner"}}}`
		output := &v0hooks.CustomAccessTokenOutput{}
		require.NoError(t, json.Unmarshal([]byte(body), output))
		require.Error(t, validateTokenClaims(output.Claims), "hook key %s must be refused", key)
	}
}
