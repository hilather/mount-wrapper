//go:build linux

package platform

import (
	"net"

	"golang.org/x/sys/unix"
)

func peerCredentials(conn net.Conn) (PeerCreds, bool) {
	fd, ok := unixConnFD(conn)
	if !ok {
		return PeerCreds{}, false
	}
	ucred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return PeerCreds{}, false
	}
	return PeerCreds{
		PID: int(ucred.Pid),
		UID: int(ucred.Uid),
		GID: int(ucred.Gid),
	}, true
}
