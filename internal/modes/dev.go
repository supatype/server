package modes

import (
	"net/http"

	"github.com/rs/cors"
)

// DevMiddleware wraps next with development-mode behaviour:
//   - Permissive CORS (reflect any Origin; required with AllowCredentials)
//   - No TLS (TLS is not applied by this wrapper; bind plain HTTP in dev)
//
// Vite HMR at /_vite/* is mounted on the chi outer mux in dev mode (see cmd.buildOuterMux)
// so requests are included in the outer JSON access log on stderr.
func DevMiddleware(next http.Handler) http.Handler {
	c := cors.New(cors.Options{
		AllowOriginFunc:  func(_ string) bool { return true },
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{"*"},
		AllowCredentials: true,
	})
	return c.Handler(next)
}
