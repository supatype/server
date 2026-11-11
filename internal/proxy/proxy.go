package proxy

import (
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
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

	// UpstreamHost, when set, is the Host header sent upstream in place of the
	// target's own host. The address the client used is in X-Forwarded-Host
	// either way.
	UpstreamHost string

	// Unreachable, when set, answers a request whose upstream could not be dialled at all, in
	// place of the bare 502 every other proxy error gets. A service that is not listening is
	// down, which its mount can say in its own words (0.9.2 release test round 2, R2-S1).
	Unreachable http.HandlerFunc
}

// DevServerProxy proxies HTTP and WebSocket requests to a developer's app
// server, naming it localhost on its own port in the Host header.
//
// 0.9.1 release test: on Docker dev the app is Vite on the host, reached as
// host.docker.internal:5173, and Vite 6.0.9 and later refuse any Host not on
// their allow list with 403 "Blocked request. This host is not allowed."
// localhost is the one name every dev server accepts by default. Only the app
// upstream gets this: the other upstreams are this server's own services,
// which expect their own names.
func DevServerProxy(target *url.URL, opts ProxyOpts) http.Handler {
	opts.UpstreamHost = localhostOn(target)
	return webSocketProxy(target, opts.UpstreamHost, New(target, opts))
}

// localhostOn is localhost with target's port, or bare localhost when target
// names none and so uses its scheme's default.
func localhostOn(target *url.URL) string {
	if port := target.Port(); port != "" {
		return net.JoinHostPort("localhost", port)
	}
	return "localhost"
}

// upstreamHostFor is the Host header for a request forwarded to target.
func upstreamHostFor(target *url.URL, override string) string {
	if override != "" {
		return override
	}
	return target.Host
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
	// Always present, so there is always a hop worth describing even when the
	// client address and host are unknown.
	parts = append(parts, "proto="+proto)
	if clientFacingHost != "" {
		parts = append(parts, "host="+rfc7239QuotedString(clientFacingHost))
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
	// Rewrite, not the deprecated Director: in Director mode the ReverseProxy
	// appends the client address to X-Forwarded-For after Director returns, so
	// the chain augmentForwardedHeaders had already extended named the client
	// twice. Rewrite mode strips the inbound forwarding headers from Out and adds
	// nothing, which leaves this hop's handling entirely to the code below.
	rp := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		carryForwardingHeaders(pr)
		rewriteForUpstream(pr.Out, pr.In.Host, target, opts)
	}}

	// Strip CORS headers from upstream: supatype-server adds its own.
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
		rp.Transport = sharedTransport(opts.RequestTimeout)
	}

	if opts.Unreachable != nil {
		rp.ErrorHandler = unreachableOrBadGateway(opts.Unreachable)
	}

	return rp
}

// transports holds one transport per upstream timeout, shared by every proxy that asks for it.
var transports sync.Map // time.Duration -> *http.Transport

// idleUpstreamConnTimeout closes a kept-alive upstream connection nothing has used for this long.
const idleUpstreamConnTimeout = 90 * time.Second

// sharedTransport is the transport for proxies with this response header timeout. The gateway
// builds a proxy per request, and each had a transport of its own, so no upstream connection was
// reused and every idle one kept its goroutines and file descriptor until the upstream closed it
// (0.9.2 release test round 2 review). Not a clone of http.DefaultTransport: that honours
// HTTP_PROXY, and these upstreams are this server's own services.
func sharedTransport(timeout time.Duration) *http.Transport {
	if t, ok := transports.Load(timeout); ok {
		return t.(*http.Transport)
	}
	t, _ := transports.LoadOrStore(timeout, &http.Transport{
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       idleUpstreamConnTimeout,
		MaxIdleConnsPerHost:   32,
	})
	return t.(*http.Transport)
}

// unreachableOrBadGateway hands a failed dial to unreachable, and answers any other proxy error
// as ReverseProxy does by default: logged, then a bare 502.
func unreachableOrBadGateway(unreachable http.HandlerFunc) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, req *http.Request, err error) {
		if isDialFailure(err) {
			unreachable(w, req)
			return
		}
		log.Printf("http: proxy error: %v", err)
		w.WriteHeader(http.StatusBadGateway)
	}
}

// isDialFailure is whether err is the transport failing to connect, refused or otherwise. An
// error after the connection opened (a reset, an EOF) is the service failing, not being down.
func isDialFailure(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// forwardingHeaders are the ones Rewrite mode strips from the outbound request.
// This hop extends what the edge sent rather than starting the chain afresh.
var forwardingHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"}

func carryForwardingHeaders(pr *httputil.ProxyRequest) {
	for _, name := range forwardingHeaders {
		if values, ok := pr.In.Header[name]; ok {
			// Shared with In rather than copied: clipped, so the Forwarded hop
			// appended later lands in a new array, and Set replaces the slice.
			pr.Out.Header[name] = slices.Clip(values)
		}
	}
}

// rewriteForUpstream points out at target, strips the route prefix, records
// this hop in the forwarding headers, then applies the configured overrides.
// Not ProxyRequest.SetURL: that joins target's path onto the request's, and the
// routes here name the upstream's root.
func rewriteForUpstream(out *http.Request, clientFacingHost string, target *url.URL, opts ProxyOpts) {
	out.URL.Scheme = target.Scheme
	out.URL.Host = target.Host
	out.Host = upstreamHostFor(target, opts.UpstreamHost)

	if opts.StripPrefix != "" {
		out.URL.Path = strings.TrimPrefix(out.URL.Path, opts.StripPrefix)
		if out.URL.Path == "" {
			out.URL.Path = "/"
		}
		out.URL.RawPath = strings.TrimPrefix(out.URL.RawPath, opts.StripPrefix)
	}

	augmentForwardedHeaders(out, clientFacingHost)

	// Static overrides first, then per-request headers, which win on conflict.
	for k, v := range opts.HeaderOverrides {
		out.Header.Set(k, v)
	}
	if opts.HeaderFunc != nil {
		for k, v := range opts.HeaderFunc(out) {
			out.Header.Set(k, v)
		}
	}
}
