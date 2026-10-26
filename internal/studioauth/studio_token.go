package studioauth

import (
	"errors"
	"maps"
	"net/http"
	"strings"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/studioctx"
)

// StudioClaim is the claim a request acting as Studio carries to PostgREST. The
// engine puts two permissive policies on every table it manages that admit it
// (`rls::STUDIO_CLAIM` in the schema engine): `read` admits reading every row,
// `write` reading and changing every row. Nothing else signs it.
const StudioClaim = studioctx.Claim

// studioTokenLifetime bounds the token a request acting as Studio forwards. It
// never leaves the gateway, and one request needs it for no longer than this.
const studioTokenLifetime = time.Minute

// The routes a request acting as Studio reaches with more than the caller's own
// token, and with what.
type studioRoute int

const (
	// studioSelf: the caller's own token, as ModeSelf. Realtime and function
	// invocation, whose upstreams have no Studio rule to apply.
	studioSelf studioRoute = iota
	// studioRows: the caller's identity with the Studio claim (mintStudioToken).
	studioRows
	// studioFiles: the service role. Storage has no field rules to keep, and its
	// own object rules are the app's, which Studio roles replace.
	studioFiles
)

// studioRouteFor is how a request acting as Studio reaches path.
func studioRouteFor(method, path string) studioRoute {
	switch {
	case under(path, "/rest/v1"), under(path, "/graphql/v1"):
		return studioRows
	case under(path, "/storage/v1/object"):
		return studioFiles
	case under(path, "/storage/v1/bucket") && isReadOnly(method):
		return studioFiles
	default:
		return studioSelf
	}
}

// isUnambiguousPath reports whether path names one resource every parser agrees
// on: no dot segment, no backslash, no percent-encoding. A request acting as
// Studio carries more than the caller's own token, and an upstream that resolves
// `/storage/v1/object/../bucket` would otherwise create a bucket with a role
// that may not.
func isUnambiguousPath(path string) bool {
	if strings.ContainsAny(path, `\%`) {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

var errNoCallerClaims = errors.New("no verified claims to act for")

// mintStudioToken is the caller's own verified claims with the Studio claim
// added, signed with the project's JWT secret, so PostgREST runs the request as
// the caller (their role, `sub` and app role, which every field rule reads) and
// the engine's Studio policies admit it to every row. `write` is the level for a
// role that may change content, `read` otherwise. Any claim in the reserved
// `supatype_` namespace the caller's token already carried, look-alikes
// included, is dropped, never trusted.
func mintStudioToken(secret string, caller jwt.MapClaims, canWrite bool, now time.Time) (string, error) {
	if len(caller) == 0 {
		return "", errNoCallerClaims
	}
	claims := maps.Clone(caller)
	for name := range claims {
		if studioctx.IsReservedClaim(name) {
			delete(claims, name)
		}
	}
	claims[StudioClaim] = studioLevel(canWrite)
	claims["iat"] = now.Unix()
	claims["exp"] = studioTokenExpiry(caller, now).Unix()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func studioLevel(canWrite bool) string {
	if canWrite {
		return "write"
	}
	return "read"
}

// studioTokenExpiry is the caller's own expiry or studioTokenLifetime from now,
// whichever is sooner: the token never outlives the session it was made from.
func studioTokenExpiry(caller jwt.MapClaims, now time.Time) time.Time {
	limit := now.Add(studioTokenLifetime)
	exp, err := caller.GetExpirationTime()
	if err != nil || exp == nil || exp.After(limit) {
		return limit
	}
	return exp.Time
}

// setStudioCredentials puts a request acting as Studio's credentials on its
// forwarded copy. It reports false, having written the response, when they
// cannot be made.
func setStudioCredentials(w http.ResponseWriter, req2 *http.Request, c Config, adm admission, path string) bool {
	if studioRouteFor(req2.Method, path) == studioFiles {
		return setServiceRole(w, req2, c)
	}
	token, err := mintStudioToken(c.JWTSecret, adm.claims, adm.perms.Write, time.Now())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not act as Studio: "+err.Error())
		return false
	}
	req2.Header.Set("Authorization", "Bearer "+token)
	setAnonKey(req2, c)
	return true
}
