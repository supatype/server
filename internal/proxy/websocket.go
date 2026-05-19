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
		defer backendConn.Close() //nolint:errcheck

		// Hijack the client connection.
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "websocket not supported", http.StatusInternalServerError)
			return
		}
		clientConn, _, err := hijacker.Hijack()
		if err != nil {
			logrus.WithError(err).Error("websocket proxy: hijack failed")
			return
		}
		defer clientConn.Close() //nolint:errcheck

		augmentForwardedHeaders(r, r.Host)

		// Forward the original HTTP Upgrade request to the backend.
		if err := r.Write(backendConn); err != nil {
			logrus.WithError(err).Error("websocket proxy: write upgrade request failed")
			return
		}

		// Splice bidirectionally.
		errc := make(chan error, 2)
		cp := func(dst io.Writer, src io.Reader) {
			_, err := io.Copy(dst, src)
			errc <- err
		}
		go cp(backendConn, clientConn)
		go cp(clientConn, backendConn)
		<-errc
	})
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
