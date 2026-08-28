package gateway

import (
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/modes"
	"github.com/supatype/server/internal/proxy"
)

// The per-mode middleware stack used to be assembled by nesting constructors,
// which reads inside out: the innermost call is the outermost middleware. The
// managed stack in particular has an order that matters and is easy to get
// backwards, and getting it backwards returns 401 to every browser preflight.
//
// Here the stack is a list, written outermost first, so the order is the order
// you read.

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain is an ordered stack of middleware, outermost first.
type Chain []Middleware

// Then wraps h in the chain. It folds from the back so that the first element
// of the slice ends up outermost, which is how the list reads.
func (c Chain) Then(h http.Handler) http.Handler {
	for i := len(c) - 1; i >= 0; i-- {
		h = c[i](h)
	}
	return h
}

// ModeChain is the middleware a deployment mode puts in front of the mux.
//
// In dev it is permissive CORS, reflecting any origin.
//
// In managed mode it is CORS, then tenant HMAC, then the project API key, and
// that order is load-bearing. A browser preflight carries neither a signature
// nor an API key, and only gets through because the CORS layer recognises the
// origin and answers the OPTIONS itself, before either gate sees it. Nest these
// the other way round and every preflight becomes a 401.
//
// In standalone mode it is the configured CORS allowlist, if there is one.
//
// Misconfiguration is logged here rather than in the mounting code, because this
// is where the consequence lives.
func ModeChain(cfg *config.Config, manifestFor func(*http.Request) *proxy.RouteManifest) Chain {
	switch cfg.Mode {
	case "dev":
		return Chain{modes.DevMiddleware}

	case "managed":
		if cfg.JWTSecret == "" {
			logrus.Error("mux: managed mode but JWT secret is unset, data-plane requests will be refused")
		}
		chain := Chain{
			func(next http.Handler) http.Handler {
				return modes.ManagedCORSMiddleware(cfg.CorsAllowOrigins, manifestFor, next)
			},
		}
		if cfg.TenantHMACSecret != "" {
			chain = append(chain, func(next http.Handler) http.Handler {
				return modes.TenantMiddleware(cfg.TenantHMACSecret, next)
			})
		} else {
			logrus.Warn("mux: managed mode but SUPATYPE_TENANT_HMAC_SECRET is unset, tenant verification disabled")
		}
		return append(chain, func(next http.Handler) http.Handler {
			return modes.APIKeyMiddleware(cfg.JWTSecret, next)
		})

	case "standalone":
		origins := modes.ParseCSV(cfg.CorsAllowOrigins)
		if len(origins) == 0 {
			return nil
		}
		return Chain{func(next http.Handler) http.Handler {
			return modes.AllowlistCORSMiddleware(func(*http.Request) []string { return origins }, next)
		}}
	}
	return nil
}
