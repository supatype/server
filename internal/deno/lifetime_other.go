//go:build !linux && !windows

package deno

import (
	"os"
	"os/exec"
)

// tieLifetime does nothing here. macOS and the BSDs have no parent-death
// signal, so a worker can outlive a server that is killed outright. A server
// that stops normally still stops its worker, through the context.
func tieLifetime(*exec.Cmd) (release func()) { return func() {} }

// adoptWorker does nothing here; see tieLifetime.
func adoptWorker(*os.Process) error { return nil }
