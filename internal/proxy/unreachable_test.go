package proxy

import (
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A failure after the connection opened is the service failing, not being down: it keeps the
// default 502, and only a failed dial goes to the unreachable answer (R2-S1).
func TestOnlyAFailedDialIsUnreachable(t *testing.T) {
	previous := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previous) })
	called := false
	handle := unreachableOrBadGateway(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	reset := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodGet, "/", nil), reset)
	if rec.Code != http.StatusBadGateway || called {
		t.Errorf("a reset answered %d (unreachable called: %v), want 502", rec.Code, called)
	}

	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	rec = httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodGet, "/", nil), refused)
	if rec.Code != http.StatusServiceUnavailable || !called {
		t.Errorf("a refused dial answered %d (unreachable called: %v), want 503", rec.Code, called)
	}
}
