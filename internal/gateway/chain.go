package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/corsorigins"
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

// ModeChain is the per-mode stack with the CORS policy cfg alone describes: what is configured,
// nothing saved and no site URL. The server builds its policy with both (see ModeChainWithPolicy).
func ModeChain(cfg *config.Config, manifestFor func(*http.Request) *proxy.RouteManifest) Chain {
	return ModeChainWithPolicy(cfg, manifestFor, DefaultCORSPolicy(cfg, nil, ""))
}

// DefaultCORSPolicy is the CORS policy for cfg: its configured and platform origins, the site URL,
// and store for a list saved from Studio (nil when there is no database).
func DefaultCORSPolicy(cfg *config.Config, store corsorigins.Store, siteURL string) *corsorigins.Policy {
	return corsorigins.NewPolicy(store, corsorigins.Defaults{
		SiteURL:    siteURL,
		Configured: corsorigins.ParseList(cfg.CorsAllowOrigins),
		Platform:   corsorigins.ParseList(cfg.CorsPlatformOrigins),
	})
}

// ModeChainWithPolicy is the middleware a deployment mode puts in front of the mux.
//
// In dev it is permissive CORS, reflecting any origin.
//
// In managed mode it is CORS, then tenant HMAC, then the project API key, and
// that order is load-bearing. A browser preflight carries neither a signature
// nor an API key, and only gets through because the CORS layer recognises the
// origin and answers the OPTIONS itself, before either gate sees it. Nest these
// the other way round and every preflight becomes a 401.
//
// In standalone mode it is the CORS policy alone, always: before SHb there was
// no layer at all when no origin was configured, and the auth service's own
// `*` went out to every origin.
//
// Misconfiguration is logged here rather than in the mounting code, because this
// is where the consequence lives.
func ModeChainWithPolicy(cfg *config.Config, manifestFor func(*http.Request) *proxy.RouteManifest, policy *corsorigins.Policy) Chain {
	corsLayer := func(next http.Handler) http.Handler {
		return modes.PolicyCORSMiddleware(policy, manifestFor, next)
	}
	switch cfg.Mode {
	case "dev":
		return Chain{modes.DevMiddleware}

	case "managed":
		if cfg.JWTSecret == "" {
			logrus.Error("mux: managed mode but JWT secret is unset, data-plane requests will be refused")
		}
		chain := Chain{corsLayer}
		if keys := tenantKeysOf(cfg); keys.Configured() {
			chain = append(chain, func(next http.Handler) http.Handler {
				return modes.TenantMiddlewareKeys(keys, next)
			})
		} else {
			logrus.Warn("mux: managed mode but no tenant signing key or secret is set, tenant verification disabled")
		}
		return append(chain, func(next http.Handler) http.Handler {
			return modes.APIKeyMiddleware(cfg.JWTSecret, next)
		})

	case "standalone":
		return Chain{corsLayer}
	}
	return nil
}

// tenantKeysOf is the tenant signature material cfg holds. The keys are used exactly as configured:
// Kong and the Cloud control plane sign with them untrimmed, so trimming here would turn a key that
// ends in a newline into a different one.
func tenantKeysOf(cfg *config.Config) modes.TenantKeys {
	return modes.TenantKeys{
		Master:       cfg.TenantMasterSecret,
		TenantKey:    cfg.TenantSigningKey,
		LegacySecret: cfg.TenantHMACSecret,
		ManagedRef:   strings.TrimSpace(cfg.ManagedProjectRef),
	}
}

// validateTenantSigning is the managed-mode startup check on the tenant signature settings: at
// least one way to verify, and a tenant key only on a gateway that knows which tenant it is.
func validateTenantSigning(cfg *config.Config) error {
	keys := tenantKeysOf(cfg)
	if !keys.Configured() {
		return errors.New("SUPATYPE_TENANT_SIGNING_KEY (a tenant gateway) or SUPATYPE_TENANT_MASTER_SECRET (the platform server) must be set in managed mode")
	}
	if modes.IsSet(keys.TenantKey) && keys.ManagedRef == "" {
		return errors.New("SUPATYPE_TENANT_SIGNING_KEY needs SUPATYPE_MANAGED_PROJECT_REF: a tenant key verifies only its own tenant")
	}
	return nil
}

// ValidateInternalEntrance is the startup check on SUPATYPE_INTERNAL_ADDR. The internal entrance
// stands in for the tenant signature of one project, so it exists only on a managed tenant gateway
// that knows its project. Anywhere else it would either do nothing or trust a project it does not
// serve, so the server refuses to start rather than run with it.
func ValidateInternalEntrance(cfg *config.Config) error {
	if strings.TrimSpace(cfg.InternalAddr) == "" {
		return nil
	}
	if cfg.Mode != "managed" {
		return fmt.Errorf("SUPATYPE_INTERNAL_ADDR is only for managed mode, and the mode is %q", cfg.Mode)
	}
	if strings.TrimSpace(cfg.ManagedProjectRef) == "" {
		return errors.New("SUPATYPE_INTERNAL_ADDR needs SUPATYPE_MANAGED_PROJECT_REF: the internal entrance serves only its own project")
	}
	return nil
}
