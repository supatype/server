package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/didip/tollbooth/v5"
	"github.com/didip/tollbooth/v5/limiter"
	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/conf"
)

// These run without a database: they build the API with nothing but its configuration.

func limitConfig(header string) *conf.GlobalConfiguration {
	cfg := &conf.GlobalConfiguration{}
	cfg.ApplyDefaults()
	cfg.RateLimitHeader = header
	return cfg
}

// allowedBeforeLimit counts how many of n requests for one key the limiter lets through.
func allowedBeforeLimit(lmt *limiter.Limiter, key string, n int) int {
	allowed := 0
	for i := 0; i < n; i++ {
		if tollbooth.LimitByKeys(lmt, []string{key}) == nil {
			allowed++
		}
	}
	return allowed
}

// A limit of N per 5 minutes lets N requests through from one client, not a fixed 30.
func TestPerClientLimitsAllowTheConfiguredNumber(t *testing.T) {
	cfg := limitConfig("X-Real-IP")
	cfg.RateLimitVerify = 3
	cfg.RateLimitOtp = 2
	lo := NewLimiterOptions(cfg)

	require.Equal(t, 3, allowedBeforeLimit(lo.Verify, "198.51.100.1", 10), "verify")
	for name, lmt := range map[string]*limiter.Limiter{
		"otp": lo.Otp, "magiclink": lo.MagicLink, "recover": lo.Recover,
		"resend": lo.Resend, "signup": lo.Signups, "user": lo.User,
	} {
		require.Equal(t, 2, allowedBeforeLimit(lmt, "198.51.100.1", 10), name)
		require.Equal(t, 2, allowedBeforeLimit(lmt, "198.51.100.2", 10), name+": another client has its own count")
	}
}

// A limit below one request still lets one through rather than refusing every request.
func TestPerClientLimitBelowOneStillAllowsOne(t *testing.T) {
	cfg := limitConfig("X-Real-IP")
	cfg.RateLimitVerify = 0.5
	require.Equal(t, 1, allowedBeforeLimit(NewLimiterOptions(cfg).Verify, "198.51.100.1", 5))
}

func requestFrom(peer, header, value string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "http://localhost/otp", nil)
	req.RemoteAddr = peer
	if value != "" {
		req.Header.Set(header, value)
	}
	return req
}

func oneRequestLimiter() *limiter.Limiter {
	return tollbooth.NewLimiter(1.0/300, &limiter.ExpirableOptions{}).SetBurst(1)
}

func TestRateLimitKey(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		first   *http.Request
		second  *http.Request
		limited bool
	}{
		{
			name:    "one client through the gateway",
			header:  "X-Real-IP",
			first:   requestFrom("172.18.0.5:40001", "X-Real-IP", "203.0.113.9"),
			second:  requestFrom("172.18.0.5:40002", "X-Real-IP", "203.0.113.9"),
			limited: true,
		},
		{
			name:    "two clients through the same gateway are counted apart",
			header:  "X-Real-IP",
			first:   requestFrom("172.18.0.5:40001", "X-Real-IP", "203.0.113.9"),
			second:  requestFrom("172.18.0.5:40002", "X-Real-IP", "203.0.113.10"),
			limited: false,
		},
		{
			name:    "a request without the header counts against its connection's address",
			header:  "X-Real-IP",
			first:   requestFrom("203.0.113.9:50001", "X-Real-IP", ""),
			second:  requestFrom("203.0.113.9:50002", "X-Real-IP", ""),
			limited: true,
		},
		{
			name:    "a header whose first value is empty counts against the connection's address",
			header:  "X-Real-IP",
			first:   requestFrom("203.0.113.9:50001", "X-Real-IP", " ,198.51.100.4"),
			second:  requestFrom("203.0.113.9:50002", "X-Real-IP", " ,198.51.100.5"),
			limited: true,
		},
		{
			name:    "no header configured counts against the connection's address",
			header:  "",
			first:   requestFrom("203.0.113.9:50001", "X-Real-IP", "198.51.100.4"),
			second:  requestFrom("203.0.113.9:50002", "X-Real-IP", "198.51.100.5"),
			limited: true,
		},
		{
			name:    "no header configured, two connections from different addresses",
			header:  "",
			first:   requestFrom("203.0.113.9:50001", "X-Real-IP", ""),
			second:  requestFrom("203.0.113.10:50001", "X-Real-IP", ""),
			limited: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &API{config: limitConfig(tc.header)}
			lmt := oneRequestLimiter()
			require.NoError(t, api.performRateLimiting(lmt, tc.first))
			err := api.performRateLimiting(lmt, tc.second)
			if tc.limited {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Token refresh, SSO, MFA and Web3 hold their configured number too, not a fixed 30.
func TestEveryLimitAllowsItsConfiguredNumber(t *testing.T) {
	cfg := limitConfig("X-Real-IP")
	cfg.RateLimitTokenRefresh = 3
	cfg.RateLimitSso = 4
	cfg.RateLimitWeb3 = 5
	cfg.MFA.RateLimitChallengeAndVerify = 2
	cfg.SAML.RateLimitAssertion = 6
	cfg.RateLimitOAuthDynamicClientRegister = 7
	lo := NewLimiterOptions(cfg)

	cases := map[string]struct {
		lmt  *limiter.Limiter
		want int
	}{
		"token":                 {lo.Token, 3},
		"sso":                   {lo.SSO, 4},
		"web3":                  {lo.Web3, 5},
		"saml assertion":        {lo.SAMLAssertion, 6},
		"oauth client register": {lo.OAuthClientRegister, 7},
		"factor verify":         {lo.FactorVerify, 2},
		"factor challenge":      {lo.FactorChallenge, 2},
	}
	for name, tc := range cases {
		require.Equal(t, tc.want, allowedBeforeLimit(tc.lmt, "198.51.100.1", 40), name)
		require.Equal(t, tc.want, allowedBeforeLimit(tc.lmt, "198.51.100.2", 40), name+": another client has its own count")
	}
}

// A limit below one request still lets one through on each of them.
func TestEveryLimitBelowOneStillAllowsOne(t *testing.T) {
	cfg := limitConfig("X-Real-IP")
	cfg.RateLimitTokenRefresh = 0.5
	cfg.RateLimitSso = 0.5
	cfg.RateLimitWeb3 = 0.5
	cfg.MFA.RateLimitChallengeAndVerify = 0.5
	cfg.SAML.RateLimitAssertion = 0.5
	cfg.RateLimitOAuthDynamicClientRegister = 0.5
	lo := NewLimiterOptions(cfg)
	for name, lmt := range map[string]*limiter.Limiter{
		"token": lo.Token, "sso": lo.SSO, "web3": lo.Web3,
		"factor verify": lo.FactorVerify, "factor challenge": lo.FactorChallenge,
		"saml assertion": lo.SAMLAssertion, "oauth client register": lo.OAuthClientRegister,
	} {
		require.Equal(t, 1, allowedBeforeLimit(lmt, "198.51.100.1", 5), name)
	}
}

// An anonymous sign-in limit below one per hour still lets one through rather than refusing all.
func TestAnonymousSignInLimitBelowOneStillAllowsOne(t *testing.T) {
	cfg := limitConfig("X-Real-IP")
	cfg.RateLimitAnonymousUsers = 0.5
	require.Equal(t, 1, allowedBeforeLimit(NewLimiterOptions(cfg).AnonymousSignIns, "198.51.100.1", 5))
}

// The anonymous limit lets the configured number through from one client.
func TestAnonymousSignInLimitAllowsTheConfiguredNumber(t *testing.T) {
	cfg := limitConfig("X-Real-IP")
	cfg.RateLimitAnonymousUsers = 4
	require.Equal(t, 4, allowedBeforeLimit(NewLimiterOptions(cfg).AnonymousSignIns, "198.51.100.1", 10))
}
