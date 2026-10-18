package studiobootstrap

import (
	"context"
	"sync"
	"time"

	"github.com/supatype/server/internal/data"
)

// classification is one answer derived from the schema snapshot, kept for a while.
//
// Each classification here is consulted on a request path and changes only on a push, so
// it is read from the snapshot at most once per TTL. They were written out one by one, each
// with its own mutex and fields, and the third would have been a third copy of the same
// rules: serve a fresh answer, keep a stale one through a failed reload, and say "unknown"
// only when there has never been one.
type classification[T any] struct {
	mu       sync.Mutex
	ttl      time.Duration
	value    T
	loadedAt time.Time
	loaded   bool
}

// get returns the memoised answer, deriving a new one from the snapshot once it is older
// than the TTL.
//
// The second result is false when there is no answer at all: the snapshot could not be
// read and there was never a previous one, or the AST could not be derived from. A failed
// read after a good one keeps serving the previous answer rather than flapping to unknown
// on a transient blip; the TTL still bounds how old it can be.
func (c *classification[T]) get(
	ctx context.Context,
	resources *data.Resources,
	derive func(ast []byte) (T, error),
) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.loaded && time.Since(c.loadedAt) < c.ttl {
		return c.value, true
	}

	var none T
	snapshot, err := LoadSnapshot(ctx, resources)
	if err != nil {
		if c.loaded {
			return c.value, true
		}
		return none, false
	}

	value, err := derive(snapshot.AST)
	if err != nil {
		return none, false
	}
	c.value, c.loadedAt, c.loaded = value, time.Now(), true
	return value, true
}

// reset forgets the answer, as if it had never been loaded.
func (c *classification[T]) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	var none T
	c.value, c.loadedAt, c.loaded = none, time.Time{}, false
}

// expire ages the answer past its TTL without clearing it, which is what a stale but
// present answer looks like when the next load fails.
func (c *classification[T]) expire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadedAt = time.Now().Add(-c.ttl - time.Second)
}
