package gateway

import (
	"net/http"

	"github.com/supatype/server/internal/changelog"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/modelhooks"
	"github.com/supatype/server/internal/proxy"
)

// WithAfterHookListener gives the gateway the change-log listener the bootstrap started, so the
// request path leaves after hooks to it once it is active.
func WithAfterHookListener(l *changelog.Listener) DepsOption {
	return func(d *Deps) { d.AfterHookListener = l }
}

// newAfterHookListener builds the change-log listener for a single-project server, or nil where it
// cannot run: no database to read, or a server answering for many tenants from one process, whose
// databases one listener cannot follow. Nil keeps after hooks on the request path.
func newAfterHookListener(
	cfg *config.Config,
	manifest func() *proxy.RouteManifest,
	inProcessDeno bool,
	perTenant bool,
) *changelog.Listener {
	dsn := cfg.SQLDSN()
	switch {
	case perTenant:
		changelog.Log().Info("per-tenant manifests: after hooks stay on the request path")
		return nil
	case dsn == "":
		changelog.Log().Info("no database DSN: after hooks stay on the request path")
		return nil
	}
	return changelog.New(listenerConfig(cfg, dsn, orEmptyManifest(manifest), inProcessDeno))
}

// listenerConfig reads every per-batch fact from the manifest the bootstrap holds.
func listenerConfig(cfg *config.Config, dsn string, manifest func() *proxy.RouteManifest, inProcessDeno bool) changelog.Config {
	return changelog.Config{
		DSN:    dsn,
		Schema: func() string { return manifest().Schema },
		Hooks: func() map[string]modelhooks.TableHooksView {
			return modelhooks.ViewsFromManifest(manifest().Hooks)
		},
		Shapes: func() modelhooks.ShapeFunc { return modelhooks.ShapesFromManifest(manifest()) },
		Resolve: func(function string) (string, error) {
			return hookUpstreamURL(cfg, manifest(), function, inProcessDeno)
		},
		Dispatcher:     modelhooks.NewDispatcher(nil, cfg.JWTSecret),
		FiredOnRequest: modelhooks.NewRequestPathMarks(cfg.JWTSecret).Verify,
	}
}

// newAfterRows is how the request path decides whether to fire an after hook and what rows to show.
// Read per request through d, so a listener supplied by option after NewDeps is still seen.
func newAfterRows(d *Deps) *modelhooks.AfterRows {
	return &modelhooks.AfterRows{
		ChangeLogActive: func() bool { return d.AfterHookListener.Active() },
		Shapes: func(req *http.Request) modelhooks.ShapeFunc {
			return modelhooks.ShapesFromManifest(d.ManifestFor(req))
		},
		Reader: modelhooks.NewRowReader(
			func(req *http.Request) string { return PostgRESTUpstream(d.ManifestFor(req), d.Config) },
			d.RestSchema,
			d.Config.ServiceRoleKey,
			nil,
		),
		Marks: modelhooks.NewRequestPathMarks(d.Config.JWTSecret),
	}
}

// orEmptyManifest never hands the listener a nil manifest, which would otherwise be a panic in a
// background goroutine with no request to fail instead.
func orEmptyManifest(manifest func() *proxy.RouteManifest) func() *proxy.RouteManifest {
	return func() *proxy.RouteManifest {
		if m := manifest(); m != nil {
			return m
		}
		return &proxy.RouteManifest{Schema: "public"}
	}
}
