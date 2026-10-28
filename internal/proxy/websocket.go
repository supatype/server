package proxy

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

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

		splice(clientConn, backendConn)
	})
}

// spliceDrainTime is how long the second direction of a spliced connection
// may keep going once the first has ended.
const spliceDrainTime = 5 * time.Second

// splice copies bytes both ways between client and backend until both
// directions have ended, then closes both connections.
//
// When one side hangs up, the end has to reach the other side: a WebSocket
// client waits for the server to close TCP after a close frame, and realtime
// keeps a socket for a client that is gone. So the first direction to end
// half-closes the connection it was writing to, passing the end along while
// the other direction still delivers what is in flight. The other direction
// then gets spliceDrainTime to finish before both are closed regardless, so
// a peer that never answers the end cannot hold the pair open.
func splice(client, backend net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		closeWrite(dst)
		done <- struct{}{}
	}
	go cp(backend, client)
	go cp(client, backend)
	<-done
	deadline := time.Now().Add(spliceDrainTime)
	_ = client.SetDeadline(deadline)
	_ = backend.SetDeadline(deadline)
	<-done
	_ = client.Close()
	_ = backend.Close()
}

// closeWrite ends the write side of conn, or the whole connection when it
// cannot be half-closed.
func closeWrite(conn net.Conn) {
	if hc, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = hc.CloseWrite()
		return
	}
	_ = conn.Close()
}

// prepareUpstreamWebSocketRequest rewrites r for forwarding over a raw TCP dial,
// mirroring rewriteForUpstream. http.Request.Write prefers RequestURI when set;
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
