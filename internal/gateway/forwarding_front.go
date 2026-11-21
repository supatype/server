package gateway

import (
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/observability"
	"github.com/supatype/server/internal/stff"
)

// forwardingFront is the outermost layer of the server: it settles the end user's address from the
// raw connection peer and the forwarding secret before anything else reads or rewrites it, and strips
// St-Forwarded-For and St-Forwarding-Secret so no service or function behind it sees them. The
// public listener and the internal entrance serve the same handler, so both follow the same rules.
//
// It reads the secret once, at startup: a reload that changes SUPATYPE_SECURITY_FORWARDING_SECRET
// takes effect on restart, the same as the gateway and the workers that hold it.
func forwardingFront(authCfg *conf.GlobalConfiguration) func(http.Handler) http.Handler {
	stff.WarnIfUnconfigured(&authCfg.Security, func(msg string) { logrus.Warn(msg) })
	return stff.Resolver(stff.Options{
		Security:        &authCfg.Security,
		RateLimitHeader: authCfg.RateLimitHeader,
		OnInvalid: func(r *http.Request, err error) {
			observability.GetLogEntry(r).Entry.WithError(err).Warn("error processing St-Forwarded-For")
		},
	})
}
