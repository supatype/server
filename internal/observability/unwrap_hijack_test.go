package observability

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

var errReachedHijack = errors.New("reached the writer underneath")

type hijackableWriter struct{ *httptest.ResponseRecorder }

func (hijackableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errReachedHijack
}

// A WebSocket upgrade behind this wrapper takes over the connection through
// http.ResponseController, which needs Unwrap to get past the wrapper.
func TestInterceptingResponseWriterUnwrapsForHijack(t *testing.T) {
	inner := hijackableWriter{httptest.NewRecorder()}
	_, _, err := http.NewResponseController(&interceptingResponseWriter{writer: inner}).Hijack()
	if !errors.Is(err, errReachedHijack) {
		t.Errorf("Hijack through interceptingResponseWriter: %v, want it to reach the writer underneath", err)
	}
}
