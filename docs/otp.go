//lint:file-ignore U1000 ignore go-swagger template
package docs

import "github.com/supatype/server/internal/auth"

// swagger:route POST /otp otp otp
// Passwordless sign-in method for email or phone.
// responses:
//   200: otpResponse

// swagger:parameters otp
type otpParamsWrapper struct {
	// Only an email or phone should be provided.
	// in:body
	Body auth.OtpParams
}

// swagger:response otpResponse
type otpResponseWrapper struct{}
