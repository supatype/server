package gateway

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
	"github.com/supatype/server/internal/deno"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
)

// defaultUpstreamHTTPTimeout caps reverse-proxy round-trips so a wedged upstream
// cannot hang requests indefinitely, including under go test.
const defaultUpstreamHTTPTimeout = 2 * time.Minute

// Build assembles the gateway: base middleware, health, the route table, and
// the per-mode stack in front of all of it.
//
// The routes are a list and this is a fold over it. What each route is, when it
// exists and how it attaches are properties of the entry in Routes; the only
// thing expressed here is that they are applied in order.
func Build(d *Deps) http.Handler {
	r := chi.NewRouter()
	for _, mw := range baseMiddleware(d.Config) {
		r.Use(mw)
	}
	outerhealth.Attach(r, d.Config, d.Version, d.HealthProbes)

	// The Studio proxy fronts every other service on this router, so it needs
	// the router before the routes are attached to it.
	d.Router = r

	for _, route := range Routes() {
		attach(r, route, d)
	}

	// The per-mode stack, outermost first. See ModeChain for why the managed
	// order is load-bearing.
	return ModeChain(d.Config, d.ManifestFor).Then(r)
}

// baseMiddleware runs on every request, whatever the mode.
func baseMiddleware(cfg *config.Config) Chain {
	return Chain{
		middleware.RequestID,
		WithOuterAccessLogContext(cfg.Mode, cfg.ManagedProjectRef),
		middleware.Recoverer,
		middleware.RequestLogger(outerAccessLogFormatter{}),
	}
}

// buildOuterMux is the positional form the bootstrap and the behaviour locks
// still call. It exists so the route table could replace the old builder
// without rewriting every caller in the same commit.
func buildOuterMux(
	cfg *config.Config,
	manifestFor func(*http.Request) *proxy.RouteManifest,
	healthProbes func() outerhealth.ProbeConfig,
	authHandler http.Handler,
	denoManager *deno.Manager,
	version string,
	resources *data.Resources,
	sendEmailHook http.Handler,
) http.Handler {
	return Build(NewDeps(
		cfg, manifestFor, healthProbes, authHandler,
		denoManager, version, resources, sendEmailHook,
	))
}
