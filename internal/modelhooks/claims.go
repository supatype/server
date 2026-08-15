package modelhooks

import (
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// ClaimsFromBearer builds a ClaimsFunc that reads the caller from the request's bearer token.
//
// Verified with the same secret PostgREST validates against, so a hook is never told about an
// identity the database will not honour. On any failure — no token, a bad signature, the project's
// anon key rather than a user token — the hook sees `null`, which is what "an anonymous caller"
// means. Guessing would be worse than saying nothing: a hook that trusts `user.sub` to decide
// something should be handed a verified subject or none at all.
//
// A token with no `sub` is anonymous by this definition. The anon and service-role keys carry a role
// and no subject, so they arrive here as null rather than as a user with an empty id.
func ClaimsFromBearer(jwtSecret string) ClaimsFunc {
	secret := strings.TrimSpace(jwtSecret)
	return func(req *http.Request) *Claims {
		if secret == "" {
			return nil
		}
		token := bearerToken(req)
		if token == "" {
			return nil
		}

		parsed := jwt.MapClaims{}
		if _, err := jwt.ParseWithClaims(token, parsed, func(t *jwt.Token) (any, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		}); err != nil {
			return nil
		}

		sub, _ := parsed["sub"].(string)
		if sub == "" {
			return nil
		}
		role, _ := parsed["role"].(string)
		email, _ := parsed["email"].(string)
		return &Claims{Sub: sub, Role: role, Email: email}
	}
}

func bearerToken(req *http.Request) string {
	header := req.Header.Get("Authorization")
	if len(header) < 7 || !strings.EqualFold(header[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}
