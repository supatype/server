package proxy

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
)

// F16 — WebSocket upgrade through WebSocketProxy (Vite HMR path uses the same helper).
func TestWebSocketProxy_UpgradeAndEcho(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected websocket", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("upstream cannot hijack")
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		defer conn.Close() //nolint:errcheck

		_, _ = io.WriteString(bufrw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = bufrw.Flush()

		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("upstream read: %v", err)
		}
		_, _ = conn.Write(buf[:n])
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler := WebSocketProxy(target, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fallback", http.StatusTeapot)
	}))

	proxySrv := httptest.NewServer(handler)
	defer proxySrv.Close()

	proxyURL, err := url.Parse(proxySrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", proxyURL.Host, 2*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close() //nolint:errcheck
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "GET /_vite/@vite/client HTTP/1.1\r\n" +
		"Host: " + proxyURL.Host + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write upgrade: %v", err)
	}

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(statusLine, "101") {
		t.Fatalf("status = %q want 101", statusLine)
	}
	// Drain response headers
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	payload := []byte("hmr-ping")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo = %q want %q", got, payload)
	}
}

// Realtime proxy mounts StripPrefix("/realtime/v1") before WebSocketProxy; URL.Path
// is stripped but RequestURI stays edge-original — upstream must see "/" not "/realtime/v1".
func TestWebSocketProxy_StripPrefixRequestURI(t *testing.T) {
	var upstreamPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected websocket", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("upstream cannot hijack")
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		defer conn.Close() //nolint:errcheck
		_, _ = io.WriteString(bufrw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = bufrw.Flush()
		_, _ = io.WriteString(conn, `{"type":"system","status":"ok","message":"authenticated"}`)
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler := http.StripPrefix("/realtime/v1", WebSocketProxy(target, http.NotFoundHandler()))

	proxySrv := httptest.NewServer(handler)
	defer proxySrv.Close()

	proxyURL, err := url.Parse(proxySrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", proxyURL.Host, 2*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close() //nolint:errcheck
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := "GET /realtime/v1?token=abc HTTP/1.1\r\n" +
		"Host: " + proxyURL.Host + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write upgrade: %v", err)
	}

	br := bufio.NewReader(conn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(statusLine, "101") {
		t.Fatalf("status = %q want 101", statusLine)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	got := make([]byte, 64)
	n, err := br.Read(got)
	if err != nil {
		t.Fatalf("read upstream payload: %v", err)
	}
	if !strings.Contains(string(got[:n]), "authenticated") {
		t.Fatalf("payload = %q want authenticated", got[:n])
	}
	if upstreamPath != "/" {
		t.Fatalf("upstream path = %q want /", upstreamPath)
	}
}
