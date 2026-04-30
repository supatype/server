package modes

import (
	"net/http"
	"net/url"

	"github.com/rs/cors"
	"github.com/supatype/auth/internal/proxy"
)

// DevMiddleware wraps next with development-mode behaviour:
//   - Permissive CORS (all origins, all methods, all headers)
//   - Vite HMR WebSocket proxy at /_vite/* forwarded to viteDevURL
//
// viteDevURL may be empty; if so the /_vite/* proxy is skipped.
func DevMiddleware(next http.Handler, viteDevURL string) http.Handler {
	c := cors.New(cors.Options{
		// AllowOriginFunc reflects the requesting origin instead of returning "*",
		// which is required when AllowCredentials is true (browsers reject wildcard+credentials).
		AllowOriginFunc:  func(_ string) bool { return true },
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{"*"},
		AllowCredentials: true,
	})

	mux := http.NewServeMux()

	if viteDevURL != "" {
		viteTarget, err := url.Parse(viteDevURL)
		if err == nil {
			viteProxy := proxy.WebSocketProxy(viteTarget, proxy.New(viteTarget, proxy.ProxyOpts{}))
			mux.Handle("/_vite/", http.StripPrefix("/_vite", viteProxy))
		}
	}

	mux.Handle("/", next)

	return c.Handler(mux)
}
