package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// maskedTextHi is a masked client text frame carrying "hi".
var maskedTextHi = []byte{0x81, 0x82, 0x01, 0x02, 0x03, 0x04, 'h' ^ 0x01, 'i' ^ 0x02}

// A client may send its first frame in the same write as the upgrade request.
// The server reads both into its buffer before the handler runs, so those
// bytes are in the hijacked reader, not on the socket, and must still reach
// the target.
func TestWebSocketProxy_ForwardsBytesSentWithTheUpgrade(t *testing.T) {
	received := make(chan []byte, 1)
	target := frameReadingTarget(t, len(maskedTextHi), received)
	proxySrv := httptest.NewServer(WebSocketProxy(target, http.NotFoundHandler()))
	t.Cleanup(proxySrv.Close)

	conn, err := net.DialTimeout("tcp", proxySrv.Listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	req := "GET /realtime/v1/websocket HTTP/1.1\r\n" +
		"Host: " + proxySrv.Listener.Addr().String() + "\r\n" +
		"Connection: Upgrade\r\nUpgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"
	if _, err := conn.Write(append([]byte(req), maskedTextHi...)); err != nil {
		t.Fatalf("write upgrade and frame: %v", err)
	}

	select {
	case got := <-received:
		if !bytes.Equal(got, maskedTextHi) {
			t.Fatalf("target got % x, want % x", got, maskedTextHi)
		}
	case <-time.After(2 * promptly):
		t.Fatal("target never received the frame sent with the upgrade")
	}
}

// frameReadingTarget answers one upgrade with 101, then reads n bytes from the
// connection and sends them on received, or what it got before it gave up.
func frameReadingTarget(t *testing.T, n int, received chan<- []byte) *url.URL {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close() //nolint:errcheck
		br := bufio.NewReader(conn)
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = conn.SetReadDeadline(time.Now().Add(promptly))
		got := make([]byte, n)
		read, _ := io.ReadFull(br, got)
		received <- got[:read]
	}()
	return &url.URL{Scheme: "http", Host: ln.Addr().String()}
}
