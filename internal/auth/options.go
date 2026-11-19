package auth

import (
	"time"

	"github.com/didip/tollbooth/v5"
	"github.com/didip/tollbooth/v5/limiter"
	"github.com/supatype/server/internal/auth/mailer"
	"github.com/supatype/server/internal/auth/tokens"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/ratelimit"
)

type Option interface {
	apply(*API)
}

type optionFunc func(*API)

func (f optionFunc) apply(a *API) { f(a) }

func WithMailer(m mailer.Mailer) Option {
	return optionFunc(func(a *API) {
		a.mailer = m
	})
}

func WithTokenService(service *tokens.Service) Option {
	return optionFunc(func(a *API) {
		a.tokenService = service
	})
}

type LimiterOptions struct {
	Email ratelimit.Limiter
	Phone ratelimit.Limiter

	Signups             *limiter.Limiter
	AnonymousSignIns    *limiter.Limiter
	Recover             *limiter.Limiter
	Resend              *limiter.Limiter
	MagicLink           *limiter.Limiter
	Otp                 *limiter.Limiter
	Token               *limiter.Limiter
	Verify              *limiter.Limiter
	User                *limiter.Limiter
	FactorVerify        *limiter.Limiter
	FactorChallenge     *limiter.Limiter
	SSO                 *limiter.Limiter
	SAMLAssertion       *limiter.Limiter
	Web3                *limiter.Limiter
	OAuthClientRegister *limiter.Limiter
}

func (lo *LimiterOptions) apply(a *API) { a.limiterOpts = lo }

func NewLimiterOptions(gc *conf.GlobalConfiguration) *LimiterOptions {
	o := &LimiterOptions{}

	o.Email = ratelimit.New(gc.RateLimitEmailSent)
	o.Phone = ratelimit.New(gc.RateLimitSmsSent)

	o.AnonymousSignIns = tollbooth.NewLimiter(gc.RateLimitAnonymousUsers/(60*60),
		&limiter.ExpirableOptions{
			DefaultExpirationTTL: time.Hour,
		}).SetBurst(burstFor(gc.RateLimitAnonymousUsers)).SetMethods([]string{"POST"})

	o.Token = newLimiterPer5mOver1h(gc.RateLimitTokenRefresh)

	o.Verify = newLimiterPer5mOver1h(gc.RateLimitVerify)

	o.FactorVerify = tollbooth.NewLimiter(gc.MFA.RateLimitChallengeAndVerify/60,
		&limiter.ExpirableOptions{
			DefaultExpirationTTL: time.Minute,
		}).SetBurst(burstFor(gc.MFA.RateLimitChallengeAndVerify))

	o.FactorChallenge = tollbooth.NewLimiter(gc.MFA.RateLimitChallengeAndVerify/60,
		&limiter.ExpirableOptions{
			DefaultExpirationTTL: time.Minute,
		}).SetBurst(burstFor(gc.MFA.RateLimitChallengeAndVerify))

	o.SSO = newLimiterPer5mOver1h(gc.RateLimitSso)

	o.SAMLAssertion = newLimiterPer5mOver1h(gc.SAML.RateLimitAssertion)

	o.Web3 = newLimiterPer5mOver1h(gc.RateLimitWeb3)

	// These all use the OTP limit per 5 min, like the ones above.
	o.Recover = newLimiterPer5mOver1h(gc.RateLimitOtp)
	o.Resend = newLimiterPer5mOver1h(gc.RateLimitOtp)
	o.MagicLink = newLimiterPer5mOver1h(gc.RateLimitOtp)
	o.Otp = newLimiterPer5mOver1h(gc.RateLimitOtp)
	o.User = newLimiterPer5mOver1h(gc.RateLimitOtp)
	o.Signups = newLimiterPer5mOver1h(gc.RateLimitOtp)
	o.OAuthClientRegister = newLimiterPer5mOver1h(gc.RateLimitOAuthDynamicClientRegister)

	return o
}

func newLimiterPer5mOver1h(rate float64) *limiter.Limiter {
	return tollbooth.NewLimiter(rate/(60*5), &limiter.ExpirableOptions{
		DefaultExpirationTTL: time.Hour,
	}).SetBurst(burstFor(rate))
}

// burstFor returns the token bucket size for a limit: the configured number
// of requests, so the limit is a real cap on a burst and not only a refill
// rate. A fractional or non-positive limit still allows one request.
func burstFor(limit float64) int {
	if limit < 1 {
		return 1
	}
	return int(limit)
}
