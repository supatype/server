package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
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

		backendConn, err := dialTarget(r.Context(), target)
		if err != nil {
			logrus.WithError(err).Error("websocket proxy: dial upstream failed")
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		clientConn, buffered, ok := hijackClient(w)
		if !ok {
			_ = backendConn.Close()
			return
		}

		prepareUpstreamWebSocketRequest(r, target, r.Host)
		if err := forwardUpgrade(backendConn, r, buffered); err != nil {
			_ = clientConn.Close()
			_ = backendConn.Close()
			logrus.WithError(err).Error("websocket proxy: write upgrade request failed")
			return
		}
		splice(clientConn, backendConn)
	})
}

// dialTimeout is how long reaching a WebSocket target may take, the TLS
// handshake included, before the request is answered 502.
var dialTimeout = 10 * time.Second

// dialTarget connects to target: over TLS for https and wss, and over plain
// TCP for anything else. The dial gives up after dialTimeout, or as soon as
// ctx, the client's request, is cancelled.
func dialTarget(ctx context.Context, target *url.URL) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: dialTimeout}
	if !isTLSTarget(target) {
		return dialer.DialContext(ctx, "tcp", targetAddr(target))
	}
	tlsDialer := &tls.Dialer{NetDialer: dialer, Config: upstreamTLSConfig(target.Hostname())}
	return tlsDialer.DialContext(ctx, "tcp", targetAddr(target))
}

// isTLSTarget reports whether target is reached over TLS.
func isTLSTarget(target *url.URL) bool {
	return target.Scheme == "https" || target.Scheme == "wss"
}

// targetAddr is the host and port to dial for target, with the scheme's
// default port when the URL names none.
func targetAddr(target *url.URL) string {
	port := "80"
	if isTLSTarget(target) {
		port = "443"
	}
	return net.JoinHostPort(target.Hostname(), portOrDefault(target, port))
}

// upstreamTLSConfig is the TLS configuration for an upstream named
// serverName. The HTTP proxy leaves its transport unset, so its upstreams are
// reached through http.DefaultTransport; this takes that transport's TLS
// settings too, so both paths trust the same roots, and fills in the server
// name as the transport would.
func upstreamTLSConfig(serverName string) *tls.Config {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if shared, ok := http.DefaultTransport.(*http.Transport); ok && shared.TLSClientConfig != nil {
		config = shared.TLSClientConfig.Clone()
	}
	if config.ServerName == "" {
		config.ServerName = serverName
	}
	return config
}

// hijackClient takes over the client connection, answering the request itself
// when it cannot. The reader holds whatever the client sent after the upgrade
// request that the server had already read off the socket.
func hijackClient(w http.ResponseWriter) (net.Conn, *bufio.Reader, bool) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket not supported", http.StatusInternalServerError)
		return nil, nil, false
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		logrus.WithError(err).Error("websocket proxy: hijack failed")
		return nil, nil, false
	}
	return conn, rw.Reader, true
}

// forwardUpgrade writes the upgrade request to the backend, then the bytes the
// client sent after it that the server had already buffered. A client may send
// its first frame in the same write as the upgrade; those bytes are no longer
// on the socket, so splicing the connection alone would drop them.
func forwardUpgrade(backend net.Conn, r *http.Request, buffered *bufio.Reader) error {
	if err := r.Write(backend); err != nil {
		return err
	}
	n := buffered.Buffered()
	if n == 0 {
		return nil
	}
	// Peeking no more than is buffered cannot fail.
	pending, _ := buffered.Peek(n)
	_, err := backend.Write(pending)
	return err
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
