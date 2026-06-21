// Package platformproxy reverse-proxies /platform/v1 to the self-host control plane sidecar.
package platformproxy

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/supatype/server/internal/functions"
	"github.com/supatype/server/internal/proxy"
)

const defaultUpstream = "http://control-plane:8080"

// Handler returns a chi router for /platform/v1/* protected by service-role auth.
func Handler() http.Handler {
	upstream := strings.TrimSpace(os.Getenv("SUPATYPE_CONTROL_PLANE_URL"))
	if upstream == "" {
		upstream = defaultUpstream
	}
	u, err := url.Parse(upstream)
	if err != nil {
		panic("platformproxy: invalid SUPATYPE_CONTROL_PLANE_URL: " + err.Error())
	}

	px := proxy.New(u, proxy.ProxyOpts{})
	r := chi.NewRouter()
	r.Use(functions.RequireServiceRoleMiddleware)
	r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		px.ServeHTTP(w, req)
	}))
	return r
}
