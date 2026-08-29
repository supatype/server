package async

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueRunsSubmittedWork(t *testing.T) {
	q := New(2, 8)
	defer q.Close()

	var wg sync.WaitGroup
	var ran atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		if !q.Submit(func() { ran.Add(1); wg.Done() }) {
			t.Fatal("submit was refused with room in the queue")
		}
	}
	wg.Wait()
	if got := ran.Load(); got != 8 {
		t.Errorf("ran %d jobs, want 8", got)
	}
	if q.Dropped() != 0 {
		t.Errorf("dropped %d with room to spare", q.Dropped())
	}
}

// Submit must never block. A caller on the request path cannot wait for
// background work, which is the reason the work is background.
func TestSubmitDropsRatherThanBlocks(t *testing.T) {
	release := make(chan struct{})
	q := New(1, 1)
	defer func() { close(release); q.Close() }()

	// Occupy the single worker, then fill the single queue slot.
	if !q.Submit(func() { <-release }) {
		t.Fatal("first submit refused")
	}
	// Give the worker a moment to pick the job up so the slot frees.
	deadline := time.Now().Add(time.Second)
	for q.Dropped() == 0 && time.Now().Before(deadline) {
		if !q.Submit(func() { <-release }) {
			break
		}
	}

	if q.Dropped() == 0 {
		t.Fatal("a saturated queue must drop rather than grow")
	}
	// The refusal is reported, not silent.
	if q.Submit(func() {}) {
		t.Log("queue had room again, which is fine; the drop above is the assertion")
	}
}

func TestCloseWaitsForQueuedWork(t *testing.T) {
	q := New(2, 16)
	var done atomic.Int64
	for i := 0; i < 16; i++ {
		q.Submit(func() {
			time.Sleep(time.Millisecond)
			done.Add(1)
		})
	}
	q.Close()
	if got := done.Load(); got != 16 {
		t.Errorf("Close returned with %d of 16 jobs finished", got)
	}
}

// Shutdown paths are not always sure whether they have already run.
func TestCloseIsIdempotentAndRefusesLaterWork(t *testing.T) {
	q := New(1, 1)
	q.Close()
	q.Close()

	if q.Submit(func() { t.Error("work ran after Close") }) {
		t.Error("a closed queue must refuse work")
	}
}

// A nil Queue is what a disabled gateway holds, and every method must tolerate it.
func TestNilQueueIsInert(t *testing.T) {
	var q *Queue
	if q.Submit(func() { t.Error("a nil queue must not run work") }) {
		t.Error("a nil queue must refuse work")
	}
	if q.Dropped() != 0 {
		t.Error("a nil queue has dropped nothing")
	}
	q.Close()
}

func TestNewClampsNonsenseBounds(t *testing.T) {
	q := New(0, -5)
	defer q.Close()

	done := make(chan struct{})
	if !q.Submit(func() { close(done) }) {
		t.Fatal("a queue built with nonsense bounds should still take one job")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the job never ran, so workers was not clamped to at least one")
	}
}
