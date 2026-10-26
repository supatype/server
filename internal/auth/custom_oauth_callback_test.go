package auth

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// authCodeURLer is the part of a loaded provider that builds the URL the
// browser is sent to, which is where the callback the provider will use shows.
type authCodeURLer interface {
	AuthCodeURL(string, ...oauth2.AuthCodeOption) string
}

// TestCustomProviderCallbackIsUnderTheAuthRoute locks where a custom provider
// sends the browser back to. The auth API is mounted at /auth/v1 behind the
// gateway, and the external URL is the gateway's root, so a callback of
// "<external URL>/callback" lands on the app route and answers 404. Built-in
// providers are configured with "<external URL>/auth/v1/callback"; custom ones
// must agree.
func (ts *CustomOAuthAdminTestSuite) TestCustomProviderCallbackIsUnderTheAuthRoute() {
	ts.createProvider(ts.createTestOAuth2Payload("callback-check"), http.StatusCreated)

	p, pConfig, err := ts.API.Provider(context.Background(), "custom:callback-check", "")
	require.NoError(ts.T(), err)

	want := ts.Config.API.ExternalURL + "/auth/v1/callback"
	require.Equal(ts.T(), want, pConfig.RedirectURI)

	authorize, ok := p.(authCodeURLer)
	require.True(ts.T(), ok, "a custom OAuth2 provider builds an authorization URL")
	u, err := url.Parse(authorize.AuthCodeURL("state"))
	require.NoError(ts.T(), err)
	require.Equal(ts.T(), want, u.Query().Get("redirect_uri"))
}

// The callback is under /auth/v1 exactly once, whichever convention the
// deployment's external URL follows: the gateway root (self-host, Cloud
// tenants) or the auth mount itself (Cloud's platform server).
func TestCustomProviderCallbackURL(t *testing.T) {
	for externalURL, want := range map[string]string{
		"https://p.example.com":            "https://p.example.com/auth/v1/callback",
		"https://p.example.com/":           "https://p.example.com/auth/v1/callback",
		"https://api.example.com/auth/v1":  "https://api.example.com/auth/v1/callback",
		"https://api.example.com/auth/v1/": "https://api.example.com/auth/v1/callback",
		"http://localhost:18473":           "http://localhost:18473/auth/v1/callback",
	} {
		require.Equal(t, want, customProviderCallbackURL(externalURL), externalURL)
	}
}
