package functions

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The relay that carries function logs from the worker to whoever is allowed to read them.
//
// Nothing here stores anything, so there is no state to assert on afterwards. What matters is that
// the bytes arrive, that they arrive as they happen rather than in one lump at the end, and that a
// deployment with no worker says so instead of holding open a stream that will never produce a line.
//
// That last one is the failure the feature was built to close. Before it, the read API existed and
// nothing wrote to it, so the tail was permanently empty and looked merely quiet. An empty stream
// and a working stream are indistinguishable from the outside unless something drives one.

// A ResponseWriter that is deliberately not an http.Flusher.
//
// httptest.ResponseRecorder implements Flush, so it cannot reach the branch that refuses to stream.
// Streaming without flushing would buffer the whole tail and deliver it when the request ends, and
// for a stream that never ends that means delivering nothing.
type unflushableWriter struct {
	header http.Header
	body   strings.Builder
	status int
}

func (u *unflushableWriter) Header() http.Header {
	if u.header == nil {
		u.header = http.Header{}
	}
	return u.header
}

func (u *unflushableWriter) Write(b []byte) (int, error) { return u.body.Write(b) }
func (u *unflushableWriter) WriteHeader(status int)      { u.status = status }

// A writer that fails on demand, for the branches that stop relaying once the reader has gone.
type failingWriter struct {
	mu     sync.Mutex
	failAt int
	writes int
}

func (f *failingWriter) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.writes >= f.failAt {
		return 0, io.ErrClosedPipe
	}
	return len(b), nil
}

type countingFlusher struct {
	mu      sync.Mutex
	flushes int
}

func (c *countingFlusher) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flushes++
}

func (c *countingFlusher) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flushes
}

// A strings.Builder guarded for the race detector, since relay writes from its own goroutine while
// the test reads.
type safeBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestNewLogStreamIsNilWithoutAWorker(t *testing.T) {
	// nil is what makes the route answer "not implemented" rather than existing and never yielding a
	// line. A deployment supervising Deno in-process has no worker to tail.
	for _, url := range []string{"", "   ", "\t\n"} {
		if got := newLogStream(url); got != nil {
			t.Errorf("newLogStream(%q) = %+v, want nil", url, got)
		}
	}
}

func TestNewLogStreamTrimsTheTrailingSlash(t *testing.T) {
	// The path is appended directly, so a kept slash would request "//functions/v1/_logs".
	got := newLogStream("  http://worker:9000///  ")
	if got == nil {
		t.Fatal("newLogStream returned nil for a real URL")
	}
	if got.workerURL != "http://worker:9000" {
		t.Errorf("workerURL = %q, want %q", got.workerURL, "http://worker:9000")
	}
	if got.client == nil {
		t.Fatal("no client, so open would panic")
	}
	// A timeout here would cut the tail off mid-flight; the point is a request that stays open.
	if got.client.Timeout != 0 {
		t.Errorf("client timeout = %v, want none: a tail is long-lived by design", got.client.Timeout)
	}
}

func TestTailWithoutAWorkerSaysSoRatherThanStreamingNothing(t *testing.T) {
	rec := httptest.NewRecorder()
	tailFunctionLogs(nil)(rec, httptest.NewRequest(http.MethodGet, "/logs/tail", nil))

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
	if !strings.Contains(rec.Body.String(), "in-process") {
		t.Errorf("body = %q, want it to explain why there is no worker", rec.Body.String())
	}
}

func TestTailRefusesToStreamThroughAWriterThatCannotFlush(t *testing.T) {
	w := &unflushableWriter{}
	tailFunctionLogs(&logStream{workerURL: "http://example.invalid", client: &http.Client{}})(
		w, httptest.NewRequest(http.MethodGet, "/logs/tail", nil))

	if w.status != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.status, http.StatusInternalServerError)
	}
	if !strings.Contains(w.body.String(), "cannot stream") {
		t.Errorf("body = %q, want it to say the server cannot stream", w.body.String())
	}
}

func TestTailReportsAWorkerItCannotReach(t *testing.T) {
	// A bad gateway, not a 500: the fault is the worker's reachability, not this server's.
	rec := httptest.NewRecorder()
	tailFunctionLogs(newLogStream("http://127.0.0.1:1"))(rec, httptest.NewRequest(http.MethodGet, "/logs/tail", nil))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if !strings.Contains(rec.Body.String(), "functions worker") {
		t.Errorf("body = %q, want it to name the worker", rec.Body.String())
	}
}

func TestTailRelaysTheWorkerStreamWithHeadersThatDefeatBuffering(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/functions/v1/_logs" {
			t.Errorf("worker path = %q, want /functions/v1/_logs", got)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q, want text/event-stream", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"message\":\"one\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"message\":\"two\"}\n\n")
	}))
	defer worker.Close()

	rec := httptest.NewRecorder()
	tailFunctionLogs(newLogStream(worker.URL))(rec, httptest.NewRequest(http.MethodGet, "/logs/tail", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Without these, anything in front of this server is free to hold the stream until it ends,
	// which for a tail is forever.
	for header, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	for _, want := range []string{"one", "two"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body %q is missing %q", rec.Body.String(), want)
		}
	}
}

func TestOpenRejectsAWorkerThatAnswersAnythingOtherThan200(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer worker.Close()

	body, err := newLogStream(worker.URL).open(context.Background())
	if err == nil {
		_ = body.Close()
		t.Fatal("open accepted a 503, so a refusal would read as an empty tail")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v, want it to carry the status", err)
	}
}

func TestOpenReportsARequestItCannotEvenBuild(t *testing.T) {
	// A control character in the URL fails inside NewRequestWithContext, before any network work.
	if _, err := (&logStream{workerURL: "http://\x7f", client: &http.Client{}}).open(context.Background()); err == nil {
		t.Fatal("open built a request from an invalid URL")
	}
}

func TestRelayStopsWhenTheReaderHasGone(t *testing.T) {
	// A reader disconnecting is the ordinary way a tail ends. If relay did not notice, the goroutine
	// reading the worker would be left running for the life of the process.
	flusher := &countingFlusher{}
	relay(context.Background(), strings.NewReader("a\nb\nc\n"), &failingWriter{failAt: 1}, flusher, time.Hour)

	if flusher.count() != 0 {
		t.Errorf("flushes = %d, want 0: nothing was written successfully", flusher.count())
	}
}

func TestRelayStopsWhenTheRequestIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		relay(ctx, strings.NewReader("a\n"), &safeBuffer{}, &countingFlusher{}, time.Hour)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("relay ignored a cancelled request and would hold the connection open")
	}
}

func TestRelayKeepsAnIdleConnectionAlive(t *testing.T) {
	// An idle tail is the normal state: a project whose functions are quiet still has a reader
	// attached. Without the comment, anything between here and that reader is free to reap the
	// connection as dead, and the next log line then arrives nowhere.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A pipe that yields nothing keeps relay in its select with no lines to carry.
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	var out safeBuffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay(ctx, pr, &out, &countingFlusher{}, 10*time.Millisecond)
	}()

	deadline := time.After(10 * time.Second)
	for {
		if strings.Contains(out.String(), ": keep-alive") {
			cancel()
			<-done
			return
		}
		select {
		case <-deadline:
			t.Fatalf("no keep-alive comment arrived; got %q", out.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestRelayStopsWhenAKeepAliveCannotBeWritten(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		relay(ctx, pr, &failingWriter{failAt: 1}, &countingFlusher{}, time.Millisecond)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("relay kept going after a keep-alive write failed")
	}
}
