package testutil

import (
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// recorder stands in for *testing.T so the failure path can be observed
// instead of failing the real test. Fatalf records the message and ends the
// goroutine the way testing.T does.
type recorder struct {
	testing.TB
	failed  atomic.Bool
	message atomic.Value
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failed.Store(true)
	r.message.Store(fmt.Sprintf(format, args...))
	runtime.Goexit()
}

func TestEventuallyReturnsAsSoonAsTheConditionHolds(t *testing.T) {
	var calls int
	Eventually(t, "the third poll", func() bool {
		calls++
		return calls == 3
	})
	if calls != 3 {
		t.Fatalf("polled %d times, want 3", calls)
	}
}

func TestEventuallyFailsNamingWhatItWaitedFor(t *testing.T) {
	r := &recorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		eventually(r, "a thing that never happens", 30*time.Millisecond, func() bool { return false })
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("eventually did not give up")
	}
	if !r.failed.Load() {
		t.Fatal("expected the wait to fail the test")
	}
	msg, _ := r.message.Load().(string)
	if !strings.Contains(msg, "a thing that never happens") {
		t.Fatalf("failure %q does not name what was awaited", msg)
	}
}
