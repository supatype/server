// Package testutil holds helpers shared by tests in more than one package.
package testutil

import (
	"testing"
	"time"
)

const (
	eventuallyTimeout = 10 * time.Second
	eventuallyPoll    = 5 * time.Millisecond
)

// Eventually polls ok until it returns true, and fails the test naming what
// was being waited for if it does not within the timeout. It lets a test wait
// for a goroutine or child process without guessing how long that takes.
func Eventually(t testing.TB, what string, ok func() bool) {
	t.Helper()
	eventually(t, what, eventuallyTimeout, ok)
}

// eventually is Eventually with the timeout passed in, so a test can prove the
// failure path without waiting ten seconds for it.
func eventually(t testing.TB, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(eventuallyPoll)
	}
	t.Fatalf("timed out waiting for %s", what)
}
