package auth

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/auth/crypto"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/auth/storage/test"
	"github.com/supatype/server/internal/conf"
)

const (
	apiTestVersion = "1"
	apiTestConfig  = "../../hack/test.env"
)

func init() {
	crypto.PasswordHashCost = crypto.QuickHashCost
}

// setupAPIForTest creates a new API to run tests with.
// Using this function allows us to keep track of the database connection
// and cleaning up data between tests.
func setupAPIForTest(opts ...Option) (*API, *conf.GlobalConfiguration, error) {
	config, err := conf.LoadGlobalKeepingEnv(apiTestConfig)
	if err != nil {
		return nil, nil, err
	}

	conn, err := test.SetupDBConnection(config)
	if err != nil {
		return nil, nil, err
	}

	return withDistinctClients(NewAPIWithVersion(config, conn, apiTestVersion, opts...)), config, nil
}

func setupAPIForTestWithCallback(cb func(*conf.GlobalConfiguration, *storage.Connection)) (*API, *conf.GlobalConfiguration, error) {
	config, err := conf.LoadGlobalKeepingEnv(apiTestConfig)
	if err != nil {
		return nil, nil, err
	}

	if cb != nil {
		cb(config, nil)
	}

	conn, err := test.SetupDBConnection(config)
	if err != nil {
		return nil, nil, err
	}

	if cb != nil {
		cb(nil, conn)
	}

	limiterOpts := NewLimiterOptions(config)
	return withDistinctClients(NewAPIWithVersion(config, conn, apiTestVersion, limiterOpts)), config, nil
}

// httptestRemoteAddr is the peer every httptest.NewRequest reports.
const httptestRemoteAddr = "192.0.2.1:1234"

var testClientSeq atomic.Uint32

// withDistinctClients gives each request that names no client a rate limit key of its own. A
// request without the rate limit header counts against the connection's address, and every httptest
// request has the same one, so a suite's requests would otherwise share one allowance and a suite
// that signs in more than the limit would be refused part way. A test about limiting names its
// client in the header, or sets RemoteAddr itself.
//
// The key goes in the rate limit header and RemoteAddr is left alone, because the address a handler
// records must stay the one the test sees: an MFA challenge stores the caller's address and verify
// refuses a different one, so a test that builds a challenge from the request, or sends the
// challenge and the verify as two requests, needs both to come from the same client. Only with no
// header configured does the key go in RemoteAddr.
func withDistinctClients(api *API) *API {
	next := api.handler
	api.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := api.config.RateLimitHeader
		if r.RemoteAddr == httptestRemoteAddr && (header == "" || r.Header.Get(header) == "") {
			n := testClientSeq.Add(1)
			addr := fmt.Sprintf("10.%d.%d.%d", n>>16&0xff, n>>8&0xff, n&0xff)
			if header != "" {
				r.Header.Set(header, addr)
			} else {
				r.RemoteAddr = addr + ":1234"
			}
		}
		next.ServeHTTP(w, r)
	})
	return api
}

func TestEmailEnabledByDefault(t *testing.T) {
	api, _, err := setupAPIForTest()
	require.NoError(t, err)

	require.True(t, api.config.External.Email.Enabled)
}

func TestOAuthServerDisabledByDefault(t *testing.T) {
	api, _, err := setupAPIForTest()
	require.NoError(t, err)

	// OAuth server should be disabled by default
	require.False(t, api.config.OAuthServer.Enabled)

	// OAuth server instance should not be initialized when disabled
	require.Nil(t, api.oauthServer)
}

func TestOAuthServerCanBeEnabled(t *testing.T) {
	api, _, err := setupAPIForTestWithCallback(func(config *conf.GlobalConfiguration, conn *storage.Connection) {
		if config != nil {
			// Enable OAuth server
			config.OAuthServer.Enabled = true
		}
	})
	require.NoError(t, err)

	// OAuth server should be enabled
	require.True(t, api.config.OAuthServer.Enabled)

	// OAuth server instance should be initialized when enabled
	require.NotNil(t, api.oauthServer)
}

// adminToken signs a token with the admin role, as the admin API requires.
func adminToken(t *testing.T, config *conf.GlobalConfiguration) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &AccessTokenClaims{Role: "supatype_admin"}).
		SignedString([]byte(config.JWT.Secret))
	require.NoError(t, err)
	return token
}
