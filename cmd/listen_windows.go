//go:build windows

package cmd

import "net"

// reusePortListenConfig returns a plain ListenConfig on Windows.
// SO_REUSEPORT is a Linux/macOS concept; Windows uses SO_REUSEADDR by default.
func reusePortListenConfig() net.ListenConfig {
	return net.ListenConfig{}
}
