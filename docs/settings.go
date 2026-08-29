//lint:file-ignore U1000 ignore go-swagger template
package docs

import "github.com/supatype/server/internal/auth"

// swagger:route GET /settings settings settings
// Returns the configuration settings for the auth service.
// responses:
//   200: settingsResponse

// swagger:response settingsResponse
type settingsResponseWrapper struct {
	// in:body
	Body auth.Settings
}
