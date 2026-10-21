// Package studioctx marks a request that Studio's proxy forwards in process, and
// keeps the Studio claim from arriving any other way.
//
// The Studio claim (`supatype_studio`) admits a request to every row of every
// managed table. Only the Studio proxy mints it, and the proxy calls the router
// in process, so a request carrying it that did not come through the proxy is
// forged or leaked. The router cannot tell the two apart from the token, which
// is why the proxy says so through the request context instead: a context value
// cannot be set by anything outside this process.
package studioctx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Claim is the claim a request acting as Studio carries to PostgREST.
const Claim = "supatype_studio"

// MaxSignedURLSeconds is the longest a signed URL made through the Studio
// proxy lives. The proxy caps the request it forwards, and a store reached in
// process caps it again from the context, so neither depends on the other.
const MaxSignedURLSeconds = 3600

// reservedPrefix is the claim namespace Supatype keeps for itself.
const reservedPrefix = "supatype_"

type proxiedKey struct{}

// WithProxied marks ctx as a request the Studio proxy is forwarding.
func WithProxied(ctx context.Context) context.Context {
	return context.WithValue(ctx, proxiedKey{}, true)
}

// IsProxied reports whether the Studio proxy is forwarding this request.
func IsProxied(ctx context.Context) bool {
	proxied, _ := ctx.Value(proxiedKey{}).(bool)
	return proxied
}

// IsReservedClaim reports whether name is in the `supatype_` namespace, after
// Unicode case folding, so `ſupatype_studio` (U+017F) and `SUPATYPE_X` count.
// A look-alike is refused rather than trusted to mean nothing downstream.
func IsReservedClaim(name string) bool {
	runes := []rune(name)
	prefix := []rune(reservedPrefix)
	if len(runes) < len(prefix) {
		return false
	}
	return strings.EqualFold(string(runes[:len(prefix)]), reservedPrefix)
}

// TokenCarriesReservedClaim reports whether a JWT's payload has a top-level
// claim in the reserved namespace. The signature is not checked: a forged token
// carrying one is refused just the same, and PostgREST would refuse it anyway.
func TokenCarriesReservedClaim(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return false
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}
	for name := range claims {
		if IsReservedClaim(name) {
			return true
		}
	}
	return false
}
