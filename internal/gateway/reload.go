package gateway

// What happens when the configuration on disk changes while the server is up.
//
// A platform drops updated files into the watch directory rather than
// restarting the pod, so the auth service is rebuilt in place: the background
// worker is told, a new API is built over the same database, and the handler
// every request goes through is swapped to it. The database connection, the
// template cache and the rate limiters are deliberately carried over — rebuilt
// ones would drop every open connection, re-read every template and reset every
// limiter's window on each edit.

import (
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/auth"
	"github.com/supatype/server/internal/auth/apiworker"
	"github.com/supatype/server/internal/auth/mailer/templatemailer"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/reloader"
	"github.com/supatype/server/internal/utilities"
)

type apiReloader struct {
	handler   *reloader.AtomicHandler
	worker    *apiworker.Worker
	db        *storage.Connection
	templates *templatemailer.Cache
	limiters  *auth.LimiterOptions
}

// Apply rebuilds the auth service over a new configuration and swaps it in.
func (r *apiReloader) Apply(latest *conf.GlobalConfiguration) {
	logrus.WithField("component", "reloader").Info("reloading api with new configuration")

	r.worker.ReloadConfig(latest)
	r.handler.Store(auth.NewAPIWithVersion(
		latest, r.db, utilities.Version,
		auth.WithMailer(templatemailer.FromConfig(latest, r.templates)),
		r.limiters,
	))
}
