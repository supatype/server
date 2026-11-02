package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

// closeFrame4401 is an unmasked server close frame carrying code 4401, as
// realtime sends it when a token expires.
var closeFrame4401 = []byte{0x88, 0x02, 0x11, 0x31}

// maskedCloseReply is the close frame a client answers with: masked, empty.
var maskedCloseReply = []byte{0x88, 0x80, 0x01, 0x02, 0x03, 0x04}

// promptly bounds how long one side may wait to see the other hang up.
const promptly = 3 * time.Second

// rawUpgradeTarget listens for one connection, answers its upgrade with 101
// and hands the connection to serve. It returns the target's URL.
func rawUpgradeTarget(t *testing.T, serve func(net.Conn)) *url.URL {
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
		if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		serve(conn)
	}()
	return &url.URL{Scheme: "http", Host: ln.Addr().String()}
}

// dialUpgraded opens a WebSocket through a proxy to target and returns the
// client connection with the 101 response already read.
func dialUpgraded(t *testing.T, target *url.URL) (net.Conn, *bufio.Reader) {
	t.Helper()
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
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write upgrade: %v", err)
	}
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(promptly))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	return conn, br
}

// waitForEOF reads r until it ends, failing the test if it has not ended
// within promptly.
func waitForEOF(t *testing.T, conn net.Conn, r io.Reader, who string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(promptly))
	_, err := io.Copy(io.Discard, r)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("%s: connection still open %s after the other side closed", who, promptly)
	}
}

// When realtime sends a close frame and hangs up, the client must see the
// frame and then the end of the connection, or a WebSocket client never fires
// close and never reconnects.
func TestWebSocketProxy_TargetCloseEndsClient(t *testing.T) {
	target := rawUpgradeTarget(t, func(conn net.Conn) {
		_, _ = conn.Write(closeFrame4401)
		// Read the client's close reply, as realtime does, then hang up.
		_ = conn.SetReadDeadline(time.Now().Add(promptly))
		_, _ = conn.Read(make([]byte, len(maskedCloseReply)))
	})
	conn, br := dialUpgraded(t, target)

	got := make([]byte, len(closeFrame4401))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("read close frame: %v", err)
	}
	if !bytes.Equal(got, closeFrame4401) {
		t.Fatalf("close frame = % x, want % x", got, closeFrame4401)
	}
	if _, err := conn.Write(maskedCloseReply); err != nil {
		t.Fatalf("write close reply: %v", err)
	}
	waitForEOF(t, conn, br, "client")
}

// A client that hangs up first must end the target side too, or realtime
// keeps a socket open for a client that is gone.
func TestWebSocketProxy_ClientCloseEndsTarget(t *testing.T) {
	targetEnded := make(chan error, 1)
	target := rawUpgradeTarget(t, func(conn net.Conn) {
		_ = conn.SetReadDeadline(time.Now().Add(promptly))
		_, err := io.Copy(io.Discard, conn)
		targetEnded <- err
	})
	conn, _ := dialUpgraded(t, target)

	if err := conn.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}
	select {
	case err := <-targetEnded:
		if errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("target: connection still open %s after the client closed", promptly)
		}
	case <-time.After(2 * promptly):
		t.Fatal("target never finished reading")
	}
}

// A connection that cannot be half-closed is closed outright, so the end
// still reaches the peer.
func TestCloseWriteClosesAConnectionThatCannotHalfClose(t *testing.T) {
	ours, peer := net.Pipe()
	defer peer.Close() //nolint:errcheck
	closeWrite(ours)
	_ = peer.SetReadDeadline(time.Now().Add(promptly))
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("peer read = %v, want EOF", err)
	}
}
