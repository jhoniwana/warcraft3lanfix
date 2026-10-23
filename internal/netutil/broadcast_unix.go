//go:build !windows

package netutil

import "syscall"

// setBroadcast sets SO_BROADCAST on a unix socket.
func setBroadcast(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
