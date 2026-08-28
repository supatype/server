package auth

import (
	"net/http"

	"github.com/gofrs/uuid"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/auth/tokens"
)

// generateAccessToken mints a token the way a handler would.
//
// Twenty test files need a token for a user, and nothing in production calls
// this: it is a one-line adaptation of tokenService.GenerateAccessToken to the
// arguments a test has to hand. It lives here rather than in token.go so it
// does not ship in the binary.
func (a *API) generateAccessToken(r *http.Request, tx *storage.Connection, user *models.User, sessionId *uuid.UUID, authenticationMethod models.AuthenticationMethod) (string, int64, error) {
	return a.tokenService.GenerateAccessToken(r, tx, tokens.GenerateAccessTokenParams{
		User:                 user,
		SessionID:            sessionId,
		AuthenticationMethod: authenticationMethod,
	})
}
