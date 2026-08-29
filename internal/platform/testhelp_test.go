package platform

import "github.com/supatype/server/internal/platform/async"

// newTestQueue gives each test its own queue so Close in one does not stop
// another, and so a test can wait for its own background work.
func newTestQueue() *async.Queue { return async.New(1, 16) }
