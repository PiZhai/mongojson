//go:build !windows

package main

import (
	"net"
)

func companionPipeListener(string) (net.Listener, error) {
	// Preserve the existing companion on non-Windows development hosts. Windows
	// uses a DACL-protected named pipe; other platforms keep loopback-only HTTP.
	return net.Listen("tcp", "127.0.0.1:18182")
}
