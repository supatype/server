package draftinsert

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
func TestHintingWriterUnwrapsForHijack(t *testing.T) {
	inner := hijackableWriter{httptest.NewRecorder()}
	_, _, err := http.NewResponseController(&hintingWriter{ResponseWriter: inner}).Hijack()
	if !errors.Is(err, errReachedHijack) {
		t.Errorf("Hijack through hintingWriter: %v, want it to reach the writer underneath", err)
	}
}
