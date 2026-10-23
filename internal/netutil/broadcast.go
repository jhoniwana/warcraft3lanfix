// Package netutil holds the small socket helpers the relay needs.
package netutil

import "net"

// EnableBroadcast allows a UDP socket to send to a broadcast address.
//
// Go does not set SO_BROADCAST on the sockets it creates, and without it
// sendto(255.255.255.255) fails with EACCES on Linux and WSAEACCES on
// Windows. Sockets that only receive do not need it.
func EnableBroadcast(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		sockErr = setBroadcast(fd)
	}); err != nil {
		return err
	}
	return sockErr
}
