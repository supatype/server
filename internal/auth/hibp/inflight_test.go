package hibp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// bodyFor returns a 200 response carrying one suffix line, enough for Check to
// parse and look up.
func bodyFor(suffix string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(suffix + ":2\r\n")),
	}
}

// One caller giving up must not fail the callers sharing its request.
//
// Requests for the same SHA-1 prefix are deduplicated into a single HTTP call,
// and the call inherited whichever context created it. A browser tab closing
// mid-signup therefore cancelled the shared request, and every other signup
// that happened to share the prefix failed with it. With FailClosed set that
// surfaces as a 500 to a user who did nothing wrong.
func TestOneCallerCancellingDoesNotFailTheOthers(t *testing.T) {
	release := make(chan struct{})
	var calls int32

	client := PwnedClient{
		HTTP: &testHTTPClient{
			Fn: func(r *http.Request) (*http.Response, error) {
				atomic.AddInt32(&calls, 1)
				// Hold the shared request open until both callers are
				// attached and the first has given up.
				<-release
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				return bodyFor("1E4C9B93F3F0682250B6CF8331B7EE68FD8"), nil
			},
		},
	}

	// "password" hashes to 5BAA6..., so both callers share a prefix.
	const password = "password"

	goneCtx, giveUp := context.WithCancel(context.Background())
	started := make(chan struct{}, 2)

	var abandonedErr, survivorErr error
	var survivorPwned bool

	wg := &sync.WaitGroup{}
	wg.Add(2)

	go func() {
		defer wg.Done()
		started <- struct{}{}
		_, abandonedErr = client.Check(goneCtx, password)
	}()

	// Let the first caller create the shared request before the second joins.
	<-started
	time.Sleep(20 * time.Millisecond)

	go func() {
		defer wg.Done()
		started <- struct{}{}
		survivorPwned, survivorErr = client.Check(context.Background(), password)
	}()

	<-started
	time.Sleep(20 * time.Millisecond)

	giveUp()
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("want the prefix deduplicated to one call, got %d", calls)
	}
	if !errors.Is(abandonedErr, context.Canceled) {
		t.Errorf("the caller that gave up should see its own cancellation, got %v", abandonedErr)
	}
	if survivorErr != nil {
		t.Errorf("the caller that waited should not inherit another's cancellation, got %v", survivorErr)
	}
	if !survivorPwned {
		t.Error("the caller that waited should have got its answer")
	}
}

// A prefix released by its last holder must not be handed to a new caller.
//
// The reference count dropped to zero outside the lock that guards the map, so
// a new caller could find the dying entry, acquire it, and be handed buffers
// that were on their way back to the pool. Under -race that is a data race;
// without it, an answer computed from another request's memory.
func TestAPrefixIsNotReusedWhileItIsBeingReleased(t *testing.T) {
	client := PwnedClient{
		HTTP: &testHTTPClient{
			Fn: func(r *http.Request) (*http.Response, error) {
				return bodyFor("1E4C9B93F3F0682250B6CF8331B7EE68FD8"), nil
			},
		},
	}

	// Hammer the same prefix so acquisitions and releases interleave.
	wg := &sync.WaitGroup{}
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				pwned, err := client.Check(context.Background(), "password")
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if !pwned {
					t.Error("the suffix is in the response, so it must be found")
					return
				}
			}
		}()
	}
	wg.Wait()

	// Every box must have been removed once its holders were done.
	client.lock.Lock()
	left := len(client.requests)
	client.lock.Unlock()
	if left != 0 {
		t.Errorf("in-flight map leaked %d entries", left)
	}
}

// A client with no HTTP client of its own uses http.DefaultClient.
//
// Driven with a context that is already cancelled, so the fallback is taken
// and the request is abandoned before anything is sent. TestEndToEnd used to
// cover this incidentally, but that one talks to the real API and is now
// opt-in, which would have left the branch untested on every ordinary run.
func TestNoHTTPClientFallsBackToTheDefault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := PwnedClient{}
	buf := &pwnedResultBuffer{Buffer: &bytes.Buffer{}}

	_, err := client.doRequest(ctx, buf, []byte("5BAA6"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want the cancelled context back, got %v", err)
	}
}
