package proxy

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/sirupsen/logrus"
)

// WebSocketProxy returns an http.Handler that proxies WebSocket connections
// to target. It detects "Connection: Upgrade" + "Upgrade: websocket", hijacks
// the connection, dials target directly, and splices the two TCP connections
// bidirectionally. Non-WebSocket requests are passed to fallback.
func WebSocketProxy(target *url.URL, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isWebSocketUpgrade(r) {
			fallback.ServeHTTP(w, r)
			return
		}

		// Dial the upstream.
		targetHost := net.JoinHostPort(target.Hostname(), portOrDefault(target, "80"))
		if target.Scheme == "https" || target.Scheme == "wss" {
			targetHost = net.JoinHostPort(target.Hostname(), portOrDefault(target, "443"))
		}

		backendConn, err := net.Dial("tcp", targetHost)
		if err != nil {
			logrus.WithError(err).Error("websocket proxy: dial upstream failed")
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}

		// Hijack the client connection.
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			_ = backendConn.Close()
			http.Error(w, "websocket not supported", http.StatusInternalServerError)
			return
		}
		clientConn, _, err := hijacker.Hijack()
		if err != nil {
			_ = backendConn.Close()
			logrus.WithError(err).Error("websocket proxy: hijack failed")
			return
		}

		clientFacingHost := r.Host
		prepareUpstreamWebSocketRequest(r, target, clientFacingHost)

		// Forward the HTTP upgrade request to the backend.
		if err := r.Write(backendConn); err != nil {
			_ = clientConn.Close()
			_ = backendConn.Close()
			logrus.WithError(err).Error("websocket proxy: write upgrade request failed")
			return
		}

		// Splice bidirectionally until both directions finish.
		errc := make(chan error, 2)
		cp := func(dst io.Writer, src io.Reader) {
			_, err := io.Copy(dst, src)
			errc <- err
		}
		go cp(backendConn, clientConn)
		go cp(clientConn, backendConn)
		<-errc
		<-errc
		_ = clientConn.Close()
		_ = backendConn.Close()
	})
}

// prepareUpstreamWebSocketRequest rewrites r for forwarding over a raw TCP dial,
// mirroring proxy.New's Director. http.Request.Write prefers RequestURI when set;
// chi StripPrefix only updates URL.Path, so we must clear RequestURI here.
func prepareUpstreamWebSocketRequest(r *http.Request, target *url.URL, clientFacingHost string) {
	r.RequestURI = ""
	r.URL.Scheme = target.Scheme
	r.URL.Host = target.Host
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}
	r.Host = target.Host
	augmentForwardedHeaders(r, clientFacingHost)
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

func portOrDefault(u *url.URL, defaultPort string) string {
	if p := u.Port(); p != "" {
		return p
	}
	return defaultPort
}
