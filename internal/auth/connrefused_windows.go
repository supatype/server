package auth

import "golang.org/x/sys/windows"

// errConnectionRefused is the error a refused TCP connection carries. On
// Windows that is WSAECONNREFUSED; syscall.ECONNREFUSED there is a value Go
// made up that no socket call returns, so matching on it never succeeds.
var errConnectionRefused error = windows.WSAECONNREFUSED
