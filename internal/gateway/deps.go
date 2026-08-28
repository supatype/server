package gateway

import (
	"context"
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/apiconfig"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
	"github.com/supatype/server/internal/data/valkey"
	"github.com/supatype/server/internal/deno"
	"github.com/supatype/server/internal/modelhooks"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/sqlrunner"
	"github.com/supatype/server/internal/studioauth"
	"github.com/supatype/server/internal/studiobootstrap"
	"github.com/supatype/server/internal/studiomembers"
)

// Deps is everything the mounts need.
//
// It exists because the builder took eight parameters and then derived another
// half-dozen values inline, which meant the derivations were interleaved with
// the mounting and each mount could quietly depend on anything declared above
// it. Deriving once, up front, is what lets the route table below be a list
// rather than a script.
type Deps struct {
	Config       *config.Config
	ManifestFor  func(*http.Request) *proxy.RouteManifest
	HealthProbes func() outerhealth.ProbeConfig
	Auth         http.Handler
	Deno         *deno.Manager
	Version      string
	Resources    *data.Resources
	SendEmail    http.Handler

	// Derived once by NewDeps.

	// APIStore is the on-disk API configuration the admin API edits and the REST
	// mount reads.
	APIStore apiconfig.Store
	// Cache is never nil; see data.Resources.Cache.
	Cache valkey.Client
	// Studio is the Studio auth configuration, including membership.
	Studio studioauth.Config
	// Hooks runs a project's schema-declared model hooks around a REST write.
	Hooks func(http.Handler) http.Handler
	// HookCallback serves previous(), or is nil when it could not be built.
	HookCallback *modelhooks.Callback
	// Baseline is the manifest with no request in hand, used by the mounts whose
	// existence is decided once at startup rather than per tenant.
	Baseline *proxy.RouteManifest

	// Router is the assembled mux. The Studio proxy fronts every service on it,
	// so it needs the router it is itself mounted on. Set by Build before the
	// mounts run.
	Router http.Handler
}

// NewDeps resolves everything the mounts share.
func NewDeps(
	cfg *config.Config,
	manifestFor func(*http.Request) *proxy.RouteManifest,
	healthProbes func() outerhealth.ProbeConfig,
	authHandler http.Handler,
	denoManager *deno.Manager,
	version string,
	resources *data.Resources,
	sendEmailHook http.Handler,
) *Deps {
	d := &Deps{
		Config:       cfg,
		ManifestFor:  manifestFor,
		HealthProbes: healthProbes,
		Auth:         authHandler,
		Deno:         denoManager,
		Version:      version,
		Resources:    resources,
		SendEmail:    sendEmailHook,
		APIStore:     apiconfig.NewFileStore(cfg.ApiConfigPath),
		Cache:        resources.Cache(),
		Baseline:     manifestFor(nil),
	}
	d.Studio = newStudioConfig(cfg, resources)
	d.HookCallback = newHookCallback(d)
	d.Hooks = newHookMiddleware(d)
	return d
}

// newStudioConfig resolves Studio capability from `_supatype.studio_members`
// rather than from the token's claims. Without a database there is nothing to
// read, so the legacy claim path stays in place instead of locking the
// deployment out of its own Studio.
func newStudioConfig(cfg *config.Config, resources *data.Resources) studioauth.Config {
	studioCfg := studioauth.ConfigFromServer(cfg)
	members := studiomembers.NewStore(resources)
	studioCfg.Members = members
	studioCfg.Resources = resources
	if members.Available() {
		studioCfg.StudioRole = members.Lookup
		logrus.Info("mux: Studio capability resolved from _supatype.studio_members")
	} else {
		logrus.Warn("mux: no database DSN, Studio capability falls back to JWT claims")
	}
	return studioCfg
}

// RestSchema is the schema a REST request resolves to: the admin API's override
// if one is set, otherwise the tenant manifest's, otherwise public.
func (d *Deps) RestSchema(req *http.Request) string {
	schema := d.ManifestFor(req).Schema
	if schema == "" {
		schema = "public"
	}
	if restCfg, err := d.APIStore.Get(req.Context()); err == nil && restCfg.Rest.Schema != "" {
		return restCfg.Rest.Schema
	}
	return schema
}

// RestMaxRows is the row cap to ask PostgREST for, or empty when the configured
// value is the default and there is nothing to say.
func (d *Deps) RestMaxRows(req *http.Request) string {
	restCfg, err := d.APIStore.Get(req.Context())
	if err != nil {
		return ""
	}
	if restCfg.Rest.MaxRows > 0 && restCfg.Rest.MaxRows != apiconfig.DefaultApiConfig().Rest.MaxRows {
		return itoa(restCfg.Rest.MaxRows)
	}
	return ""
}

// MaskedFields binds the schema's read-restriction classification to this
// gateway's resources, so the middleware that writes the header takes the
// answer as a value rather than reaching for a database of its own.
func (d *Deps) MaskedFields(ctx context.Context) (map[string][]studiobootstrap.FieldMask, bool) {
	return studiobootstrap.MaskedFields(ctx, d.Resources)
}

// AdminPool hands out the admin pool as the narrow interface the SQL runner
// asks for. Resolved per request, not at mount time: a deployment with no
// database answers 503 rather than failing to start.
//
// The conversion is explicit because a typed nil pointer in an interface is not
// a nil interface, and the runner tests its argument for neither.
func (d *Deps) AdminPool() (sqlrunner.Pool, error) {
	pool, err := d.Resources.AdminPool()
	if err != nil {
		return nil, err
	}
	return pool, nil
}
