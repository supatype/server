package proxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// silentTarget is a wss target that accepts the TCP connection and then never
// answers, so a TLS handshake with it waits for as long as the dial lets it.
func silentTarget(t *testing.T) *url.URL {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return &url.URL{Scheme: "wss", Host: ln.Addr().String()}
}

// withDialTimeout sets how long a dial may take for the length of the test.
func withDialTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	previous := dialTimeout
	t.Cleanup(func() { dialTimeout = previous })
	dialTimeout = d
}

// dialWithin dials target with ctx and fails the test unless the dial fails
// within limit.
func dialWithin(t *testing.T, ctx context.Context, target *url.URL, limit time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		conn, err := dialTarget(ctx, target)
		if err == nil {
			_ = conn.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("dial to a silent target succeeded")
		}
	case <-time.After(limit):
		t.Fatalf("dial still running after %s", limit)
	}
}

// A target that never answers fails the dial once dialTimeout has passed,
// rather than holding the request open for good.
func TestWebSocketDialGivesUpOnASilentTarget(t *testing.T) {
	withDialTimeout(t, 100*time.Millisecond)
	dialWithin(t, context.Background(), silentTarget(t), promptly)
}

// A client that goes away stops the dial its request started.
func TestWebSocketDialStopsWhenTheRequestIsCancelled(t *testing.T) {
	withDialTimeout(t, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	dialWithin(t, ctx, silentTarget(t), promptly)
}

// The proxy answers 502 when the dial to a silent target gives up.
func TestWebSocketProxyAnswersBadGatewayWhenTheDialTimesOut(t *testing.T) {
	withDialTimeout(t, 100*time.Millisecond)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	WebSocketProxy(silentTarget(t), http.NotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
}
