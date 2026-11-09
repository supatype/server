//go:build !windows

package auth

import "syscall"

// errConnectionRefused is the error a refused TCP connection carries.
var errConnectionRefused error = syscall.ECONNREFUSED
