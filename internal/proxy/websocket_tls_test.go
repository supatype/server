package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// tlsEchoUpstream is a TLS server that answers a WebSocket upgrade with 101
// and echoes the first bytes it reads back.
func tlsEchoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected websocket", http.StatusBadRequest)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close() //nolint:errcheck
		_, _ = io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		_ = conn.SetReadDeadline(time.Now().Add(promptly))
		buf := make([]byte, 64)
		n, err := rw.Read(buf)
		if err != nil {
			return
		}
		_, _ = conn.Write(buf[:n])
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

// trustForUpstreams makes the shared HTTP transport, whose TLS settings the
// HTTP proxy uses for every upstream, trust the given test server. The
// WebSocket proxy must take its TLS settings from the same place.
func trustForUpstreams(t *testing.T, upstream *httptest.Server) {
	t.Helper()
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	shared := previous.(*http.Transport).Clone()
	shared.TLSClientConfig = upstream.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = shared
}

// A wss or https target is reached over TLS, verified with the settings the
// HTTP proxy uses. Dialing it over plain TCP sends the upgrade in the clear to
// a TLS server, which answers with an error instead of 101.
func TestWebSocketProxy_ReachesATLSTargetOverTLS(t *testing.T) {
	upstream := tlsEchoUpstream(t)
	trustForUpstreams(t, upstream)
	for _, scheme := range []string{"https", "wss"} {
		t.Run(scheme, func(t *testing.T) {
			target := &url.URL{Scheme: scheme, Host: upstream.Listener.Addr().String()}
			conn, br := dialUpgraded(t, target)
			if _, err := io.WriteString(conn, "ping"); err != nil {
				t.Fatalf("write: %v", err)
			}
			got := make([]byte, 4)
			if _, err := io.ReadFull(br, got); err != nil || string(got) != "ping" {
				t.Fatalf("echo = %q, %v; want ping", got, err)
			}
		})
	}
}

// The port a target is dialed on when its URL names none.
func TestWebSocketTargetDefaultPorts(t *testing.T) {
	for raw, want := range map[string]string{
		"http://realtime":     "realtime:80",
		"ws://realtime":       "realtime:80",
		"https://realtime":    "realtime:443",
		"wss://realtime":      "realtime:443",
		"wss://realtime:8443": "realtime:8443",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := targetAddr(u); got != want {
			t.Errorf("targetAddr(%s) = %s, want %s", raw, got, want)
		}
	}
}
