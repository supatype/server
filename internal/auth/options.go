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

	o.AnonymousSignIns = tollbooth.NewLimiter(gc.RateLimitAnonymousUsers/(60*60),
		&limiter.ExpirableOptions{
			DefaultExpirationTTL: time.Hour,
		}).SetBurst(int(gc.RateLimitAnonymousUsers)).SetMethods([]string{"POST"})

	o.Token = tollbooth.NewLimiter(gc.RateLimitTokenRefresh/(60*5),
		&limiter.ExpirableOptions{
			DefaultExpirationTTL: time.Hour,
		}).SetBurst(30)

	o.Verify = newPerClientLimiter(gc.RateLimitVerify)

	o.FactorVerify = tollbooth.NewLimiter(gc.MFA.RateLimitChallengeAndVerify/60,
		&limiter.ExpirableOptions{
			DefaultExpirationTTL: time.Minute,
		}).SetBurst(30)

	o.FactorChallenge = tollbooth.NewLimiter(gc.MFA.RateLimitChallengeAndVerify/60,
		&limiter.ExpirableOptions{
			DefaultExpirationTTL: time.Minute,
		}).SetBurst(30)

	o.SSO = tollbooth.NewLimiter(gc.RateLimitSso/(60*5),
		&limiter.ExpirableOptions{
			DefaultExpirationTTL: time.Hour,
		}).SetBurst(30)

	o.SAMLAssertion = tollbooth.NewLimiter(gc.SAML.RateLimitAssertion/(60*5),
		&limiter.ExpirableOptions{
			DefaultExpirationTTL: time.Hour,
		}).SetBurst(30)

	o.Web3 = tollbooth.NewLimiter(gc.RateLimitWeb3/(60*5),
		&limiter.ExpirableOptions{
			DefaultExpirationTTL: time.Hour,
		}).SetBurst(30)

	// Sign-ups and sign-ins share the OTP limit: each endpoint counts its own requests per client.
	o.Recover = newPerClientLimiter(gc.RateLimitOtp)
	o.Resend = newPerClientLimiter(gc.RateLimitOtp)
	o.MagicLink = newPerClientLimiter(gc.RateLimitOtp)
	o.Otp = newPerClientLimiter(gc.RateLimitOtp)
	o.User = newPerClientLimiter(gc.RateLimitOtp)
	o.Signups = newPerClientLimiter(gc.RateLimitOtp)
	o.OAuthClientRegister = newLimiterPer5mOver1h(gc.RateLimitOAuthDynamicClientRegister)

	return o
}

// newPerClientLimiter allows perFiveMinutes requests from one client every 5 minutes. The bucket
// holds that many, so a client gets the configured number at once and then one more each time
// 5 minutes / perFiveMinutes passes. A fixed burst of 30 let 30 through whatever the limit was, so
// a limit below 30 never refused a burst. At least one request is always allowed.
func newPerClientLimiter(perFiveMinutes float64) *limiter.Limiter {
	burst := max(1, int(math.Ceil(perFiveMinutes)))
	return tollbooth.NewLimiter(perFiveMinutes/(60*5), &limiter.ExpirableOptions{
		DefaultExpirationTTL: time.Hour,
	}).SetBurst(burst)
}

func newLimiterPer5mOver1h(rate float64) *limiter.Limiter {
	freq := rate / (60 * 5)
	lim := tollbooth.NewLimiter(freq, &limiter.ExpirableOptions{
		DefaultExpirationTTL: time.Hour,
	}).SetBurst(30)
	return lim
}
