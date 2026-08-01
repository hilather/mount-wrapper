//go:build darwin

package platform

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerCredentials on Darwin uses LOCAL_PEERCRED (xucred). PID is always -1
// because xucred does not include a process id. Callers must treat PID == -1
// as unknown. (Python may prefer socket.getpeereid when present; Go's
// golang.org/x/sys/unix exposes GetsockoptXucred instead.)
func peerCredentials(conn net.Conn) (PeerCreds, bool) {
	fd, ok := unixConnFD(conn)
	if !ok {
		return PeerCreds{}, false
	}
	xucred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return PeerCreds{}, false
	}
	gid := -1
	if xucred.Ngroups > 0 && len(xucred.Groups) > 0 {
		gid = int(xucred.Groups[0])
	}
	return PeerCreds{
		PID: -1,
		UID: int(xucred.Uid),
		GID: gid,
	}, true
}
