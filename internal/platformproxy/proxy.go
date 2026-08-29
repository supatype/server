// Package platformproxy reverse-proxies /platform/v1 to the self-host control plane sidecar.
package platformproxy

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/functions"
	"github.com/supatype/server/internal/proxy"
)

// Handler returns a chi router for /platform/v1/* protected by service-role auth.
//
// An unusable upstream is returned as an error rather than a panic. This used to
// panic while constructing the mux, which turns one mistyped variable into a
// process that cannot start and reports it as a crash instead of a config error.
func Handler(cfg *config.Config) (http.Handler, error) {
	// Empty means "not configured", which has always meant the default rather
	// than an error. A Config built directly instead of through config.Load has
	// no tag defaults applied, and losing the mount in that case is how the
	// route-table lock caught this.
	upstream := strings.TrimSpace(cfg.ControlPlaneURL)
	if upstream == "" {
		upstream = config.DefaultControlPlaneURL
	}
	u, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("platformproxy: SUPATYPE_CONTROL_PLANE_URL %q: %w", upstream, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("platformproxy: SUPATYPE_CONTROL_PLANE_URL %q needs a scheme and host", upstream)
	}

	px := proxy.New(u, proxy.ProxyOpts{})
	r := chi.NewRouter()
	r.Use(functions.RequireServiceRoleMiddleware(cfg))
	r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		px.ServeHTTP(w, req)
	}))
	return r, nil
}
