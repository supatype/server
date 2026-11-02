package proxy

import (
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// tlsEchoUpstream is a TLS server that answers a WebSocket upgrade with 101
// and echoes the first bytes it reads back. With h2 it also offers HTTP/2 in
// its TLS handshake, as a realtime server behind a modern TLS front does.
func tlsEchoUpstream(t *testing.T, h2 bool) *httptest.Server {
	t.Helper()
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	upstream.EnableHTTP2 = h2
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	return upstream
}

// trustForUpstreams makes both proxies trust the given test server: the
// shared HTTP transport, which the HTTP proxy uses for every upstream, and
// upstreamRoots, which the WebSocket proxy verifies against.
func trustForUpstreams(t *testing.T, upstream *httptest.Server) {
	t.Helper()
	previous, previousRoots := http.DefaultTransport, upstreamRoots
	t.Cleanup(func() { http.DefaultTransport, upstreamRoots = previous, previousRoots })
	shared := previous.(*http.Transport).Clone()
	shared.TLSClientConfig = upstream.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = shared
	upstreamRoots = x509.NewCertPool()
	upstreamRoots.AddCert(upstream.Certificate())
}

// A wss or https target is reached over TLS, verified with the settings the
// HTTP proxy uses. Dialing it over plain TCP sends the upgrade in the clear to
// a TLS server, which answers with an error instead of 101.
func TestWebSocketProxy_ReachesATLSTargetOverTLS(t *testing.T) {
	upstream := tlsEchoUpstream(t, false)
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

// A WebSocket upgrade is HTTP/1.1, so the TLS handshake must offer only
// HTTP/1.1. Once the HTTP proxy has made a request, the shared transport's
// TLS settings offer h2 as well; a WebSocket dial that took them as they are
// would agree on h2 with an upstream that speaks it, then write an HTTP/1.1
// upgrade the upstream reads as a broken HTTP/2 preface.
func TestWebSocketProxy_UpgradesOverHTTP1WithAnH2Upstream(t *testing.T) {
	upstream := tlsEchoUpstream(t, true)
	trustForUpstreams(t, upstream)
	resp, err := http.Get(upstream.URL)
	if err != nil {
		t.Fatalf("plain request through the shared transport: %v", err)
	}
	_ = resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("the upstream answered over %s, want HTTP/2 so the test means something", resp.Proto)
	}

	conn, br := dialUpgraded(t, &url.URL{Scheme: "wss", Host: upstream.Listener.Addr().String()})
	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo = %q, %v; want ping", got, err)
	}
}
