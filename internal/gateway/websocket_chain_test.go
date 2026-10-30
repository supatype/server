package gateway

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/proxy"
)

// echoUpgradeServer answers a WebSocket upgrade with 101 and echoes the first bytes it reads.
func echoUpgradeServer(t *testing.T) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, bufrw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Errorf("upstream hijack: %v", err)
			return
		}
		defer conn.Close() //nolint:errcheck
		_, _ = io.WriteString(bufrw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = bufrw.Flush()
		buf := make([]byte, 64)
		if n, err := bufrw.Read(buf); err == nil {
			_, _ = conn.Write(buf[:n])
		}
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// upgradeAndEcho sends an upgrade with an Origin header to host, then a frame, and returns the
// status line and what came back.
func upgradeAndEcho(t *testing.T, host string) (status, echoed string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", host, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "GET /realtime/v1/websocket HTTP/1.1\r\nHost: "+host+"\r\nOrigin: https://app.example\r\n"+
		"Connection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	br := bufio.NewReader(conn)
	status, _ = br.ReadString('\n')
	for line, err := br.ReadString('\n'); err == nil && line != "\r\n"; line, err = br.ReadString('\n') {
	}
	_, _ = io.WriteString(conn, "frame")
	buf := make([]byte, 5)
	n, _ := io.ReadFull(br, buf)
	return strings.TrimSpace(status), string(buf[:n])
}

// Realtime reaches the client through the mode's middleware. Outside dev mode the CORS layer
// wraps every response writer, and the proxy has to reach the connection underneath it to take
// it over; it used to ask the wrapper directly and answer 500 "websocket not supported".
func TestAWebSocketUpgradeGetsThroughTheModeChain(t *testing.T) {
	target := echoUpgradeServer(t)
	for _, mode := range []string{"standalone", "dev"} {
		t.Run(mode, func(t *testing.T) {
			cfg := &config.Config{Mode: mode, CorsAllowOrigins: "https://app.example"}
			chain := ModeChain(cfg, func(*http.Request) *proxy.RouteManifest { return nil })
			handler := chain.Then(proxy.WebSocketProxy(target, http.NotFoundHandler()))
			srv := httptest.NewServer(handler)
			defer srv.Close()

			status, echoed := upgradeAndEcho(t, strings.TrimPrefix(srv.URL, "http://"))
			if !strings.Contains(status, "101") || echoed != "frame" {
				t.Errorf("status %q, echoed %q; want 101 and the frame back", status, echoed)
			}
		})
	}
}
