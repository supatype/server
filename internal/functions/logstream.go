package functions

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/utilities"
)

// A live tail of function logs, relayed from the worker that produces them.
//
// # Why a relay and not a store
//
// `RecentLogs` on the local Deno manager answers `GET /{name}/logs`, and it only exists when this
// server supervises Deno itself. In Compose and on cloud the functions run in a separate worker
// container, so that source is nil and the route returns nothing: the read API was there and
// nothing wrote to it. That was the whole of the F13 gap.
//
// The worker now writes one JSON object per line and offers its own tail, so this relays that
// stream to whoever is authorised to read it. Nothing is stored here. Cloud already retains logs in
// Loki with per-tier retention; self-host keeps a live tail and adds no service and no table.
//
// # Why it goes through the server at all
//
// The worker is reachable only inside the compose network, and it authenticates nobody. A function
// logs whatever its author decided to log, which can include anything the function touched, so the
// tail is at least as sensitive as the project's data. Relaying it through the admin router means it
// inherits `RequireServiceRoleMiddleware` rather than needing an authentication story of its own.
type logStream struct {
	workerURL string
	client    *http.Client
}

// newLogStream returns nil when this deployment has no external worker to tail, which is what makes
// the route absent rather than empty in that case.
func newLogStream(workerURL string) *logStream {
	trimmed := strings.TrimRight(strings.TrimSpace(workerURL), "/")
	if trimmed == "" {
		return nil
	}
	return &logStream{
		workerURL: trimmed,
		// No timeout: this request is a stream that stays open for as long as the reader wants it.
		// The default client's timeout would cut the tail off mid-flight.
		client: &http.Client{},
	}
}

// tailFunctionLogs relays the worker's event stream for as long as the client stays connected.
func tailFunctionLogs(stream *logStream) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if stream == nil {
			// Not an error: a deployment running functions in-process has no worker to tail, and the
			// caller should be told that plainly rather than getting an empty stream that never ends.
			utilities.WriteJSON(w, http.StatusNotImplemented, map[string]string{
				"error": "this deployment runs functions in-process, so there is no worker log stream",
			})
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			utilities.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "this server cannot stream"})
			return
		}

		upstream, err := stream.open(r.Context())
		if err != nil {
			logrus.WithError(err).Warn("functions: could not open the worker log stream")
			utilities.WriteJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach the functions worker"})
			return
		}
		defer func() { _ = upstream.Close() }()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Kong and any other proxy in front of this must not buffer a stream whose whole purpose is
		// to arrive as it happens.
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		relay(r.Context(), upstream, w, flusher, keepAliveInterval)
	}
}

// How often an idle tail emits a comment. A parameter on relay rather than a literal inside it so a
// test can drive that branch without waiting twenty seconds for it.
const keepAliveInterval = 20 * time.Second

func (s *logStream) open(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.workerURL+"/functions/v1/_logs", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")

	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, errors.New("worker answered " + res.Status)
	}
	return res.Body, nil
}

// relay copies lines through, flushing each one, and sends a comment periodically so an idle tail
// is not mistaken for a dead connection by anything between here and the reader.
func relay(ctx context.Context, upstream io.Reader, w io.Writer, flusher http.Flusher, keepAliveEvery time.Duration) {
	lines := make(chan string)
	go func() {
		defer close(lines)
		reader := bufio.NewReader(upstream)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	keepAlive := time.NewTicker(keepAliveEvery)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case line, open := <-lines:
			if !open {
				return
			}
			if _, err := io.WriteString(w, line); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			// A comment line: valid SSE, ignored by every client, and enough to keep an idle
			// connection from being reaped.
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
