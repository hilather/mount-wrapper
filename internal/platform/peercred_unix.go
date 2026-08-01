//go:build unix

package platform

import "net"

// unixConnFD extracts the integer file descriptor from a *net.UnixConn.
func unixConnFD(conn net.Conn) (int, bool) {
	uc, ok := conn.(*net.UnixConn)
	if !ok || uc == nil {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, false
	}
	var fd int
	err = raw.Control(func(sysfd uintptr) {
		fd = int(sysfd)
	})
	if err != nil || fd < 0 {
		return 0, false
	}
	return fd, true
}
