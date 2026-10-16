package provider

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/conf"
)

func keycloakAuthorizeScopes(t *testing.T, extra string) []string {
	t.Helper()
	p, err := NewKeycloakProvider(conf.OAuthProviderConfiguration{
		Enabled:     true,
		ClientID:    []string{"client"},
		Secret:      "secret",
		RedirectURI: "https://app.example.com/callback",
		URL:         "https://kc.example.com/realms/app/",
	}, extra)
	require.NoError(t, err)

	u, err := url.Parse(p.AuthCodeURL("state"))
	require.NoError(t, err)
	assert.Equal(t, "/realms/app/protocol/openid-connect/auth", u.Path)
	return strings.Fields(u.Query().Get("scope"))
}

// Keycloak 26 answers userinfo with 403 unless the token was issued for the
// openid scope, and a realm need not add it by default. Without it every
// Keycloak sign-in failed at "Error getting user profile from external provider".
func TestKeycloakAuthorizeURLRequestsOpenID(t *testing.T) {
	assert.Equal(t, []string{"profile", "email", "openid"}, keycloakAuthorizeScopes(t, ""))
}

func TestKeycloakKeepsConfiguredScopesWithoutRepeatingOpenID(t *testing.T) {
	assert.Equal(t,
		[]string{"profile", "email", "openid", "roles"},
		keycloakAuthorizeScopes(t, "openid,roles"))
}
