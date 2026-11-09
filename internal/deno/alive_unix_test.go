//go:build unix

package deno

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processAlive reports whether pid names a process that has not exited. A
// zombie has exited: it is only waiting for whoever adopted it to reap it.
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		// No /proc (macOS): signal 0 succeeding is the best answer there is.
		return true
	}
	// The state follows the command name, which is in parentheses.
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) == 0 || fields[0] != "Z"
}
