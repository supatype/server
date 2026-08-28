// Package async runs background work off the request path, with a ceiling.
//
// The metering it exists for used to be `go doSomething()` per request. That is
// fine until the control plane slows down: each call then holds a goroutine for
// its whole timeout, arrivals keep starting more, and a gateway under load
// answers a downstream slowdown by growing without limit. The work is
// best-effort billing telemetry, so the right response to saturation is to drop
// it and say so, not to keep queueing.
package async

import (
	"sync"
	"sync/atomic"
)

// Queue runs submitted work on a fixed number of goroutines.
//
// The zero value is not usable; build one with New.
type Queue struct {
	jobs    chan func()
	wg      sync.WaitGroup
	dropped atomic.Int64
	closing atomic.Bool
}

// New starts workers goroutines reading from a queue depth slots deep.
//
// Both bounds are deliberate. workers caps how much of the downstream service
// this process can occupy at once; depth is the burst it will absorb before it
// starts dropping rather than growing.
func New(workers, depth int) *Queue {
	if workers < 1 {
		workers = 1
	}
	// At least one slot. An unbuffered channel would make Submit succeed only
	// when a worker happens to be parked at that instant, so an idle queue would
	// drop work for no reason.
	if depth < 1 {
		depth = 1
	}
	q := &Queue{jobs: make(chan func(), depth)}
	q.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer q.wg.Done()
			for job := range q.jobs {
				job()
			}
		}()
	}
	return q
}

// Submit queues work, returning false when the queue is full or closed and the
// work was dropped.
//
// It never blocks. A caller on the request path must not wait for background
// work, which is the whole reason the work is background.
func (q *Queue) Submit(job func()) bool {
	if q == nil || q.closing.Load() {
		return false
	}
	select {
	case q.jobs <- job:
		return true
	default:
		q.dropped.Add(1)
		return false
	}
}

// Dropped is how much work has been shed. Non-zero means the queue is too small
// or the downstream service is too slow, and the telemetry is now incomplete.
func (q *Queue) Dropped() int64 {
	if q == nil {
		return 0
	}
	return q.dropped.Load()
}

// Close stops accepting work and waits for what is already queued.
//
// Safe to call more than once, because shutdown paths are not always sure
// whether they have already run.
func (q *Queue) Close() {
	if q == nil || !q.closing.CompareAndSwap(false, true) {
		return
	}
	close(q.jobs)
	q.wg.Wait()
}
