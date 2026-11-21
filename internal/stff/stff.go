package stff

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/supatype/server/internal/conf"
)

// HeaderName is the St-Forwarded-For header name. It is all lowercase here as HTTP header names
// are not case-sensitive.
const HeaderName = "st-forwarded-for"

// ctxKey types the context keys. Not &struct{}{}: pointers to distinct zero-size values may compare
// equal, which made two such keys one.
type ctxKey int

const (
	ctxKeySTFF ctxKey = iota
	ctxKeyResolution
)

var ErrHeaderInvalid = errors.New("invalid St-Forwarded-For header value")

func parseSTFFHeader(headerVal string) (string, error) {
	values := strings.SplitN(headerVal, ",", 2)
	key := strings.TrimSpace(values[0])
	if ipAddr := net.ParseIP(key); ipAddr != nil {
		return ipAddr.String(), nil
	}

	return "", ErrHeaderInvalid
}

// GetIPAddress returns the St-Forwarded-For address the resolver believed with no forwarding secret
// configured and SUPATYPE_SECURITY_ST_FORWARDED_FOR_ENABLED on. If no value is present in the
// request context, this function will return ("", false).
func GetIPAddress(r *http.Request) (addr string, found bool) {
	if ipAddr, ok := r.Context().Value(ctxKeySTFF).(string); ok && ipAddr != "" {
		return ipAddr, true
	}

	return "", false
}

// Middleware is Resolver with no rate-limit header, for callers that only need St-Forwarded-For.
func Middleware(cfg *conf.SecurityConfiguration, errCallback func(*http.Request, error)) func(http.Handler) http.Handler {
	return Resolver(Options{Security: cfg, OnInvalid: errCallback})
}
