package stff

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"github.com/supatype/server/internal/conf"
)

// SecretHeaderName carries the forwarding secret, SUPATYPE_SECURITY_FORWARDING_SECRET, which only
// this stack's own gateway and workers hold.
const SecretHeaderName = "St-Forwarding-Secret"

// UnconfiguredWarning is logged once at startup when no forwarding secret is set.
const UnconfiguredWarning = "SUPATYPE_SECURITY_FORWARDING_SECRET is not set: St-Forwarded-For, the " +
	"SUPATYPE_RATE_LIMIT_HEADER header and X-Forwarded-For are believed from any sender for rate " +
	"limits and audit logs. Set it, and give it to the gateway and the functions worker, so only " +
	"this stack's own components can name the end user's address."

// resolution is the client address the resolver settled on for one request.
type resolution struct {
	addr string
	// decided is set when a forwarding secret is configured: addr then replaces every other source
	// for rate limits and audit logs, because nothing else on the request can be believed.
	decided bool
}

// Options configures Resolver.
type Options struct {
	Security *conf.SecurityConfiguration
	// RateLimitHeader is SUPATYPE_RATE_LIMIT_HEADER, the gateway's X-Real-IP.
	RateLimitHeader string
	// OnInvalid hears about an St-Forwarded-For that is not an IP address. Optional.
	OnInvalid func(*http.Request, error)
}

// Resolver settles who the client is, once per request, and strips St-Forwarded-For and
// St-Forwarding-Secret so neither is passed on to a service or to user code.
//
// It must run before anything rewrites RemoteAddr from X-Forwarded-For, because with a secret
// configured the raw connection peer is the only address an untrusted request has. A request the
// outer layer already resolved passes straight through, so the auth router mounting it again inside
// the gateway changes nothing.
func Resolver(opts Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if _, done := r.Context().Value(ctxKeyResolution).(resolution); done {
				next.ServeHTTP(rw, r)
				return
			}
			next.ServeHTTP(rw, resolve(r, opts))
		})
	}
}

func resolve(r *http.Request, opts Options) *http.Request {
	presented := r.Header.Get(SecretHeaderName)
	forwarded := r.Header.Get(HeaderName)
	r.Header.Del(SecretHeaderName)
	r.Header.Del(HeaderName)

	peer := RemoteHost(r.RemoteAddr)
	ctx := r.Context()
	if secret := opts.Security.ForwardingSecret; secret != "" {
		addr := peer
		if secretMatches(presented, secret) {
			addr = firstNonEmpty(validForwarded(r, forwarded, opts.OnInvalid), firstValue(r.Header, opts.RateLimitHeader), peer)
		}
		return r.WithContext(context.WithValue(ctx, ctxKeyResolution, resolution{addr: addr, decided: true}))
	}

	// No secret: believe what the sender says, as before it existed.
	var stffAddr string
	if opts.Security.StForwardedForEnabled {
		stffAddr = validForwarded(r, forwarded, opts.OnInvalid)
	}
	if stffAddr != "" {
		ctx = context.WithValue(ctx, ctxKeySTFF, stffAddr)
	}
	addr := firstNonEmpty(stffAddr, firstValue(r.Header, opts.RateLimitHeader), peer)
	return r.WithContext(context.WithValue(ctx, ctxKeyResolution, resolution{addr: addr}))
}

// secretMatches compares digests, so the comparison takes the same time whatever the lengths.
func secretMatches(presented, secret string) bool {
	if presented == "" {
		return false
	}
	a := sha256.Sum256([]byte(presented))
	b := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// validForwarded is the leftmost St-Forwarded-For address, or empty when there is none or it is
// not an IP address.
func validForwarded(r *http.Request, value string, onInvalid func(*http.Request, error)) string {
	if value == "" {
		return ""
	}
	addr, err := parseSTFFHeader(value)
	if err != nil && onInvalid != nil {
		onInvalid(r, err)
	}
	return addr
}

// firstValue is the first comma-separated value of header, which is the one a single-address
// header such as X-Real-IP carries (RFC 7230 section 3.2.2).
func firstValue(h http.Header, header string) string {
	if header == "" {
		return ""
	}
	first, _, _ := strings.Cut(h.Get(header), ",")
	return strings.TrimSpace(first)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// RemoteHost is remoteAddr without its port; every new connection has a new source port, so a limit
// keyed on host:port would give each connection a fresh allowance.
func RemoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// ClientAddress is the end user's address as the resolver settled it, for passing on to a hook or
// the functions worker in St-Forwarded-For.
func ClientAddress(ctx context.Context) (string, bool) {
	res, ok := ctx.Value(ctxKeyResolution).(resolution)
	return res.addr, ok && res.addr != ""
}

// DecidedAddress is the client address when a forwarding secret is configured. Rate limits and
// audit logs use it in place of every header, which an untrusted sender could have written.
func DecidedAddress(r *http.Request) (string, bool) {
	res, ok := r.Context().Value(ctxKeyResolution).(resolution)
	return res.addr, ok && res.decided && res.addr != ""
}

// WarnIfUnconfigured calls warn once, with UnconfiguredWarning, when no forwarding secret is set.
func WarnIfUnconfigured(sec *conf.SecurityConfiguration, warn func(string)) {
	if sec.ForwardingSecret == "" {
		warn(UnconfiguredWarning)
	}
}
