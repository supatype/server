package auth

import (
	"testing"

	"github.com/didip/tollbooth/v5"
	"github.com/stretchr/testify/assert"
	"github.com/supatype/server/internal/conf"
)

func TestNewLimiterOptions(t *testing.T) {
	cfg := &conf.GlobalConfiguration{}
	cfg.ApplyDefaults()

	rl := NewLimiterOptions(cfg)
	assert.NotNil(t, rl.Email)
	assert.NotNil(t, rl.Phone)
	assert.NotNil(t, rl.Signups)
	assert.NotNil(t, rl.AnonymousSignIns)
	assert.NotNil(t, rl.Recover)
	assert.NotNil(t, rl.Resend)
	assert.NotNil(t, rl.MagicLink)
	assert.NotNil(t, rl.Otp)
	assert.NotNil(t, rl.Token)
	assert.NotNil(t, rl.Verify)
	assert.NotNil(t, rl.User)
	assert.NotNil(t, rl.FactorVerify)
	assert.NotNil(t, rl.FactorChallenge)
	assert.NotNil(t, rl.SSO)
	assert.NotNil(t, rl.SAMLAssertion)
}

func TestLimiterBurstEqualsConfiguredLimit(t *testing.T) {
	cfg := &conf.GlobalConfiguration{}
	cfg.ApplyDefaults()
	cfg.RateLimitVerify = 2
	cfg.RateLimitTokenRefresh = 150
	cfg.RateLimitSso = 7
	cfg.RateLimitWeb3 = 9
	cfg.RateLimitOtp = 4
	cfg.RateLimitOAuthDynamicClientRegister = 3
	cfg.MFA.RateLimitChallengeAndVerify = 15
	cfg.SAML.RateLimitAssertion = 11

	o := NewLimiterOptions(cfg)

	cases := map[string]struct {
		got  int
		want int
	}{
		"verify":              {o.Verify.GetBurst(), 2},
		"token":               {o.Token.GetBurst(), 150},
		"sso":                 {o.SSO.GetBurst(), 7},
		"web3":                {o.Web3.GetBurst(), 9},
		"saml assertion":      {o.SAMLAssertion.GetBurst(), 11},
		"factor verify":       {o.FactorVerify.GetBurst(), 15},
		"factor challenge":    {o.FactorChallenge.GetBurst(), 15},
		"otp":                 {o.Otp.GetBurst(), 4},
		"recover":             {o.Recover.GetBurst(), 4},
		"resend":              {o.Resend.GetBurst(), 4},
		"magic link":          {o.MagicLink.GetBurst(), 4},
		"user":                {o.User.GetBurst(), 4},
		"signups":             {o.Signups.GetBurst(), 4},
		"oauth client create": {o.OAuthClientRegister.GetBurst(), 3},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, c.got, name)
	}
}

func TestLimiterRefusesRequestPastConfiguredLimit(t *testing.T) {
	cfg := &conf.GlobalConfiguration{}
	cfg.ApplyDefaults()
	cfg.RateLimitVerify = 2

	o := NewLimiterOptions(cfg)
	keys := []string{"203.0.113.9"}
	assert.Nil(t, tollbooth.LimitByKeys(o.Verify, keys), "1st request")
	assert.Nil(t, tollbooth.LimitByKeys(o.Verify, keys), "2nd request")
	assert.NotNil(t, tollbooth.LimitByKeys(o.Verify, keys), "3rd request must be refused")
}

func TestLimiterBurstIsAtLeastOne(t *testing.T) {
	cfg := &conf.GlobalConfiguration{}
	cfg.ApplyDefaults()
	cfg.RateLimitVerify = 0.5
	cfg.RateLimitOtp = 0

	o := NewLimiterOptions(cfg)
	assert.Equal(t, 1, o.Verify.GetBurst())
	assert.Equal(t, 1, o.Otp.GetBurst())
}
