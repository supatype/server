package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/publicenv"
	"github.com/supatype/server/internal/site"
	"github.com/supatype/server/internal/static"
)

// ValidateAppListener is the startup check on SUPATYPE_APP_ADDR. The app listener serves one
// project's public site, so it exists only on a managed tenant gateway that knows its project, and
// needs somewhere to keep the site and somewhere to download it from. The byte budget comes from
// the site pointer, so SUPATYPE_SITE_MAX_BYTES is optional and only a fallback. Anywhere else the server
// refuses to start rather than run with a listener that would do nothing or serve the wrong thing.
func ValidateAppListener(cfg *config.Config) error {
	if strings.TrimSpace(cfg.AppAddr) == "" {
		return nil
	}
	if cfg.Mode != "managed" {
		return fmt.Errorf("SUPATYPE_APP_ADDR is only for managed mode, and the mode is %q", cfg.Mode)
	}
	if strings.TrimSpace(cfg.ManagedProjectRef) == "" {
		return errors.New("SUPATYPE_APP_ADDR needs SUPATYPE_MANAGED_PROJECT_REF: the app listener serves only its own project")
	}
	if strings.TrimSpace(cfg.SiteDir) == "" {
		return errors.New("SUPATYPE_APP_ADDR needs SUPATYPE_SITE_DIR, the folder the site is kept in")
	}
	if strings.TrimSpace(cfg.SiteSourceURL) == "" {
		return errors.New("SUPATYPE_APP_ADDR needs SUPATYPE_SITE_SOURCE_URL, where the site is downloaded from")
	}
	if cfg.SiteMaxBytes < 0 {
		return errors.New("SUPATYPE_SITE_MAX_BYTES must not be negative")
	}
	return nil
}

// NewAppHandler is the app listener's router: the base middleware, the two public env routes, and
// siteHandler for everything else. It has no tenant middleware and mounts no API, admin, studio or
// platform route, so none of them is reachable on the project's public host.
func NewAppHandler(cfg *config.Config, siteHandler http.Handler, opts ...DepsOption) http.Handler {
	d := &Deps{Config: cfg}
	for _, opt := range opts {
		opt(d)
	}
	r := chi.NewRouter()
	for _, mw := range baseMiddleware(cfg) {
		r.Use(mw)
	}
	env := buildPublicEnv(d)
	r.Handle(publicenv.JSONPath, env)
	r.Handle(publicenv.ScriptPath, env)
	r.Handle("/*", siteHandler)
	return r
}

// newSiteSyncer makes the syncer that follows the live site pointer: every manifest that becomes live
// tells it which deployment is wanted. It is set before the first manifest is merged, so a pod that
// starts with a site live begins syncing at once.
func newSiteSyncer(cfg *config.Config, live *liveManifests) (*site.Syncer, error) {
	syncer, err := site.New(site.Config{
		Dir:       strings.TrimSpace(cfg.SiteDir),
		SourceURL: strings.TrimSpace(cfg.SiteSourceURL),
		TenantID:  strings.TrimSpace(cfg.ManagedProjectRef),
		MaxBytes:  cfg.SiteMaxBytes,
		Cache:     func() static.CacheOpts { return staticCacheOpts(cfg, live.For(nil)) },
		// A request that began on a version has finished within a minute.
		RetireAfter: time.Minute,
	})
	if err != nil {
		return nil, err
	}
	live.onInstall = func(m *proxy.RouteManifest) { syncer.Want(m.Site) }
	return syncer, nil
}
