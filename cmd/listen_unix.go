//go:build linux || darwin

package cmd

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// reusePortListenConfig returns a ListenConfig that sets SO_REUSEPORT on the
// socket, allowing multiple processes to bind the same port (useful for
// zero-downtime restarts).
func reusePortListenConfig() net.ListenConfig {
	return net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1) // #nosec G115
			}); err != nil {
				return err
			}
			return serr
		},
	}
}
