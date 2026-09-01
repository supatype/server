package stff

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/supatype/server/internal/conf"
)

// HeaderName is the St-Forwarded-For header name. It is all lowercase here as HTTP header names
// are not case-sensitive.
const HeaderName = "st-forwarded-for"

var (
	ctxKeySTFF = &struct{}{}

	ErrHeaderNotFound = errors.New("St-Forwarded-For header not found")
	ErrHeaderInvalid  = errors.New("invalid St-Forwarded-For header value")
)

func parseSTFFHeader(headerVal string) (string, error) {
	values := strings.SplitN(headerVal, ",", 2)
	key := strings.TrimSpace(values[0])
	if ipAddr := net.ParseIP(key); ipAddr != nil {
		return ipAddr.String(), nil
	}

	return "", ErrHeaderInvalid
}

// GetIPAddress returns the value of the IP address in St-Forwarded-For as defined by
// SBForwardedForMiddleware. If no value is present in the request context, this function will
// return ("", false).
func GetIPAddress(r *http.Request) (addr string, found bool) {
	if ipAddr, ok := r.Context().Value(ctxKeySTFF).(string); ok && ipAddr != "" {
		return ipAddr, true
	}

	return "", false
}

// withIPAddress parses the St-Forwarded-For header and adds the leftmost value to the
// request context if it is a valid IP address, then returns a new request with modified context.
// If the leftmost value is not a valid IP address or the header is not set, this function returns
// an error.
func withIPAddress(r *http.Request) (*http.Request, error) {
	headerVal := r.Header.Get(HeaderName)
	if headerVal == "" {
		return nil, ErrHeaderNotFound
	}

	parsedIPAddr, err := parseSTFFHeader(headerVal)
	if err != nil {
		return nil, err
	}

	ctx := r.Context()
	newCtx := context.WithValue(ctx, ctxKeySTFF, parsedIPAddr)
	out := r.WithContext(newCtx)

	return out, nil
}

// Middleware returns a middleware function that parses the St-Forwarded-For header
// and adds the leftmost header value to the request context if SUPATYPE_SECURITY_ST_FORWARDED_FOR_ENABLED
// is true and the value is a valid IP address.
func Middleware(cfg *conf.SecurityConfiguration, errCallback func(*http.Request, error)) func(http.Handler) http.Handler {
	out := func(next http.Handler) http.Handler {
		handlerFunc := func(rw http.ResponseWriter, r *http.Request) {
			if !cfg.StForwardedForEnabled {
				next.ServeHTTP(rw, r)
				return
			}

			reqWithSTFF, err := withIPAddress(r)
			switch {
			case err == nil:
				next.ServeHTTP(rw, reqWithSTFF)
			case errors.Is(err, ErrHeaderNotFound):
				next.ServeHTTP(rw, r)
			default:
				errCallback(r, err)
				next.ServeHTTP(rw, r)
			}
		}

		return http.HandlerFunc(handlerFunc)
	}

	return out
}
