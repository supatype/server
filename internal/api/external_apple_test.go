package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/supatype/auth/internal/api/provider"
)

// appleDiscoveryClient returns an *http.Client that serves Apple's OIDC
// discovery document from memory. Apple ignores the provider URL override and
// performs live discovery against appleid.apple.com; injecting this client via
// oidc.ClientContext lets the authorize redirect be built without network
// access (and without widening any production API).
func appleDiscoveryClient() *http.Client {
	const discovery = `{
		"issuer": "https://appleid.apple.com",
		"authorization_endpoint": "https://appleid.apple.com/auth/authorize",
		"token_endpoint": "https://appleid.apple.com/auth/token",
		"jwks_uri": "https://appleid.apple.com/auth/keys",
		"response_types_supported": ["code"],
		"id_token_signing_alg_values_supported": ["RS256"],
		"subject_types_supported": ["pairwise"]
	}`
	return &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if !strings.Contains(r.URL.Path, "/.well-known/openid-configuration") {
				return nil, fmt.Errorf("unexpected request to %s", r.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(discovery)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		}),
	}
}

func (ts *ExternalTestSuite) TestSignupExternalApple() {
	req := httptest.NewRequest(http.MethodGet, "http://localhost/authorize?provider=apple", nil)
	// Serve Apple's OIDC discovery from memory so this runs without network.
	req = req.WithContext(oidc.ClientContext(req.Context(), appleDiscoveryClient()))
	defer ts.API.oidcCache.Invalidate(provider.DefaultAppleIssuer)
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
