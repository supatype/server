package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/supatype/auth/internal/api/provider"
)

func (ts *ExternalTestSuite) TestSignupExternalApple() {
	// Apple ignores the provider URL override, so it would normally perform live
	// OIDC discovery against appleid.apple.com. Pre-warm the cache with an
	// offline provider built from Apple's static endpoints so the authorize
	// redirect can be produced without network access.
	ts.API.oidcCache.Set(provider.DefaultAppleIssuer, (&oidc.ProviderConfig{
		IssuerURL: provider.DefaultAppleIssuer,
		AuthURL:   "https://appleid.apple.com/auth/authorize",
		TokenURL:  "https://appleid.apple.com/auth/token",
		JWKSURL:   "https://appleid.apple.com/auth/keys",
	}).NewProvider(context.Background()))

	req := httptest.NewRequest(http.MethodGet, "http://localhost/authorize?provider=apple", nil)
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	ts.Require().Equal(http.StatusFound, w.Code)
	u, err := url.Parse(w.Header().Get("Location"))
	ts.Require().NoError(err, "redirect url parse failed")
	q := u.Query()
	ts.Equal(ts.Config.External.Apple.RedirectURI, q.Get("redirect_uri"))
	ts.Equal(ts.Config.External.Apple.ClientID, []string{q.Get("client_id")})
	ts.Equal("code", q.Get("response_type"))
	ts.Equal("email name", q.Get("scope"))

	assertValidOAuthState(ts, q.Get("state"), "apple")
}
