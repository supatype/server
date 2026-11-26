package auth

import (
	"math"
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

	o.AnonymousSignIns = newClientLimiter(gc.RateLimitAnonymousUsers, time.Hour, time.Hour).SetMethods([]string{"POST"})

	o.Token = newPerClientLimiter(gc.RateLimitTokenRefresh)

	o.Verify = newPerClientLimiter(gc.RateLimitVerify)

	o.FactorVerify = newClientLimiter(gc.MFA.RateLimitChallengeAndVerify, time.Minute, time.Minute)

	o.FactorChallenge = newClientLimiter(gc.MFA.RateLimitChallengeAndVerify, time.Minute, time.Minute)

	o.SSO = newPerClientLimiter(gc.RateLimitSso)

	o.SAMLAssertion = newPerClientLimiter(gc.SAML.RateLimitAssertion)

	o.Web3 = newPerClientLimiter(gc.RateLimitWeb3)

	// Sign-ups and sign-ins share the OTP limit: each endpoint counts its own requests per client.
	o.Recover = newPerClientLimiter(gc.RateLimitOtp)
	o.Resend = newPerClientLimiter(gc.RateLimitOtp)
	o.MagicLink = newPerClientLimiter(gc.RateLimitOtp)
	o.Otp = newPerClientLimiter(gc.RateLimitOtp)
	o.User = newPerClientLimiter(gc.RateLimitOtp)
	o.Signups = newPerClientLimiter(gc.RateLimitOtp)
	o.OAuthClientRegister = newPerClientLimiter(gc.RateLimitOAuthDynamicClientRegister)

	return o
}

// newClientLimiter allows limit requests from one client per window. The bucket holds that many,
// so a client gets the configured number at once and then one more each time window / limit passes.
// A fixed burst of 30 let 30 through whatever the limit was, so a limit below 30 never refused a
// burst; every configured limit now goes through here. At least one request is always allowed.
// ttl is how long an idle client's bucket is kept.
func newClientLimiter(limit float64, window, ttl time.Duration) *limiter.Limiter {
	burst := max(1, int(math.Ceil(limit)))
	return tollbooth.NewLimiter(limit/window.Seconds(), &limiter.ExpirableOptions{
		DefaultExpirationTTL: ttl,
	}).SetBurst(burst)
}

// newPerClientLimiter is newClientLimiter for the limits counted per 5 minutes.
func newPerClientLimiter(perFiveMinutes float64) *limiter.Limiter {
	return newClientLimiter(perFiveMinutes, 5*time.Minute, time.Hour)
}
