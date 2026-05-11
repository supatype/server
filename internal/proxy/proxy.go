package proxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// ProxyOpts configures the behaviour of a reverse proxy handler.
type ProxyOpts struct {
	// StripPrefix removes this prefix from the request path before forwarding.
	StripPrefix string

	// HeaderOverrides sets (or replaces) these headers on every forwarded request.
	HeaderOverrides map[string]string

	// HeaderFunc, when set, is called per-request and its return value is merged
	// with HeaderOverrides (HeaderFunc takes precedence on key conflicts).
	HeaderFunc func(*http.Request) map[string]string

	// RequestTimeout caps the upstream round-trip duration.
	RequestTimeout time.Duration
}

// augmentForwardedHeaders sets X-Forwarded-For (client IP only), X-Forwarded-Proto,
// and X-Forwarded-Host. clientFacingHost is the Host the edge received before rewriting
// req.Host to the upstream.
func augmentForwardedHeaders(req *http.Request, clientFacingHost string) {
	clientIP := clientIPFromRemoteAddr(req.RemoteAddr)

	if clientIP != "" {
		xff := clientIP
		if prior, ok := req.Header["X-Forwarded-For"]; ok {
			xff = strings.Join(prior, ", ") + ", " + xff
		}
		req.Header.Set("X-Forwarded-For", xff)
	}

	if req.Header.Get("X-Forwarded-Proto") == "" {
		req.Header.Set("X-Forwarded-Proto", forwardedProto(req))
	}

	if req.Header.Get("X-Forwarded-Host") == "" && clientFacingHost != "" {
		req.Header.Set("X-Forwarded-Host", clientFacingHost)
	}

	// RFC 7239: append one forwarded-element for this hop (separate field-value;
	// parsers combine multiple Forwarded header lines).
	addRFC7239ForwardedHop(req, clientIP, clientFacingHost)
}

func clientIPFromRemoteAddr(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func forwardedProto(req *http.Request) string {
	if req.TLS != nil {
		return "https"
	}
	return "http"
}

// addRFC7239ForwardedHop appends a Forwarded header value describing this proxy hop.
func addRFC7239ForwardedHop(req *http.Request, clientIP, clientFacingHost string) {
	proto := req.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = forwardedProto(req)
	}

	var parts []string
	if clientIP != "" {
		parts = append(parts, "for="+rfc7239ForParam(clientIP))
	}
	parts = append(parts, "proto="+proto)
	if clientFacingHost != "" {
		parts = append(parts, "host="+rfc7239QuotedString(clientFacingHost))
	}
	if len(parts) == 0 {
		return
	}
	req.Header.Add("Forwarded", strings.Join(parts, ";"))
}

func rfc7239ForParam(ip string) string {
	if ip == "" {
		return ""
	}
	if parsed := net.ParseIP(ip); parsed != nil {
		if v4 := parsed.To4(); v4 != nil {
			return v4.String()
		}
		return rfc7239QuotedString("[" + parsed.String() + "]")
	}
	// Non-IP (e.g. unix socket path): use quoted opaque node-name.
	return rfc7239QuotedString(ip)
}

func rfc7239QuotedString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' || c == '"' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}

// New returns an http.Handler that reverse-proxies requests to target.
// The handler forwards X-Forwarded-For, X-Forwarded-Proto, X-Forwarded-Host, and RFC 7239
// Forwarded (one appended hop),
// strips CORS headers from the upstream response (supatype-server is the sole
// source of CORS truth), and adds any HeaderOverrides before forwarding.
func New(target *url.URL, opts ProxyOpts) http.Handler {
	rp := httputil.NewSingleHostReverseProxy(target)

	// Replace the default director to get full control over header handling.
	rp.Director = func(req *http.Request) {
		clientFacingHost := req.Host

		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host

		if opts.StripPrefix != "" {
			req.URL.Path = strings.TrimPrefix(req.URL.Path, opts.StripPrefix)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
			req.URL.RawPath = strings.TrimPrefix(req.URL.RawPath, opts.StripPrefix)
		}

		augmentForwardedHeaders(req, clientFacingHost)

		// Apply static header overrides first.
		for k, v := range opts.HeaderOverrides {
			req.Header.Set(k, v)
		}
		// Apply per-request headers (take precedence over HeaderOverrides).
		if opts.HeaderFunc != nil {
			for k, v := range opts.HeaderFunc(req) {
				req.Header.Set(k, v)
			}
		}
	}

	// Strip CORS headers from upstream — supatype-server adds its own.
	rp.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Access-Control-Allow-Origin")
		resp.Header.Del("Access-Control-Allow-Methods")
		resp.Header.Del("Access-Control-Allow-Headers")
		resp.Header.Del("Access-Control-Expose-Headers")
		resp.Header.Del("Access-Control-Allow-Credentials")
		resp.Header.Del("Access-Control-Max-Age")
		return nil
	}

	if opts.RequestTimeout > 0 {
		transport := &http.Transport{}
		rp.Transport = &timeoutTransport{inner: transport, timeout: opts.RequestTimeout}
	}

	return rp
}

type timeoutTransport struct {
	inner   http.RoundTripper
	timeout time.Duration
}

func (t *timeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Attach a deadline to the request context.
	ctx := req.Context()
	var cancel func()
	ctx, cancel = context.WithTimeout(ctx, t.timeout)
	defer cancel()
	return t.inner.RoundTrip(req.WithContext(ctx))
}
