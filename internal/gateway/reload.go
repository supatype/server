package gateway

// What happens when the configuration on disk changes while the server is up.
//
// A platform drops updated files into the watch directory rather than
// restarting the pod, so the auth service is rebuilt in place: the background
// worker is told, a new API is built over the same database, and the handler
// every request goes through is swapped to it. The database connection, the
// template cache and the rate limiters are deliberately carried over, because rebuilt
// ones would drop every open connection, re-read every template and reset every
// limiter's window on each edit. The two exceptions are the email send cap and the
// CORS site URL, which follow the new configuration (see refresh).

import (
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/auth"
	"github.com/supatype/server/internal/auth/apiworker"
	"github.com/supatype/server/internal/auth/mailer/templatemailer"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/corsorigins"
	"github.com/supatype/server/internal/reloader"
	"github.com/supatype/server/internal/utilities"
)

type apiReloader struct {
	handler   *reloader.AtomicHandler
	worker    *apiworker.Worker
	db        *storage.Connection
	templates *templatemailer.Cache
	limiters  *auth.LimiterOptions
	emailRate conf.Rate
	cors      *corsorigins.Policy
}

// Apply rebuilds the auth service over a new configuration and swaps it in.
func (r *apiReloader) Apply(latest *conf.GlobalConfiguration) {
	logrus.WithField("component", "reloader").Info("reloading api with new configuration")

	r.worker.ReloadConfig(latest)
	limiters := r.refresh(latest)
	r.handler.Store(auth.NewAPIWithVersion(
		latest, r.db, utilities.Version,
		auth.WithMailer(templatemailer.FromConfig(latest, r.templates)),
		limiters,
	))
}

// refresh is the part of a reload that rebuilds nothing: it returns the limiters
// the new API should use and moves the CORS policy to the new site URL. The email
// limiter is rebuilt only when the cap changed, so an edit to anything else keeps
// its count; every other limiter is carried over.
func (r *apiReloader) refresh(latest *conf.GlobalConfiguration) *auth.LimiterOptions {
	if r.cors != nil {
		r.cors.SetSiteURL(latest.SiteURL)
	}
	if latest.RateLimitEmailSent != r.emailRate {
		r.limiters = r.limiters.WithEmailRate(latest.RateLimitEmailSent)
		r.emailRate = latest.RateLimitEmailSent
	}
	return r.limiters
}
