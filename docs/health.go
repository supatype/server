//lint:file-ignore U1000 ignore go-swagger template
package docs

import "github.com/supatype/server/internal/auth"

// swagger:route GET /health health health
// The healthcheck endpoint. Returns the current version.
// responses:
//   200: healthCheckResponse

// swagger:response healthCheckResponse
type healthCheckResponseWrapper struct {
	// in:body
	Body auth.HealthCheckResponse
}
