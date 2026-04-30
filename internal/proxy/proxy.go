package proxy

import (
	"context"
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

// New returns an http.Handler that reverse-proxies requests to target.
// The handler forwards X-Forwarded-For, X-Forwarded-Proto, and X-Forwarded-Host,
// strips CORS headers from the upstream response (supatype-server is the sole
// source of CORS truth), and adds any HeaderOverrides before forwarding.
func New(target *url.URL, opts ProxyOpts) http.Handler {
	rp := httputil.NewSingleHostReverseProxy(target)

	// Replace the default director to get full control over header handling.
	rp.Director = func(req *http.Request) {
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

		// Propagate the original client address.
		if clientIP := req.RemoteAddr; clientIP != "" {
			if prior, ok := req.Header["X-Forwarded-For"]; ok {
				clientIP = strings.Join(prior, ", ") + ", " + clientIP
			}
			req.Header.Set("X-Forwarded-For", clientIP)
		}

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
