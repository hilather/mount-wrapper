//go:build !linux && !darwin

package platform

import "net"

func peerCredentials(conn net.Conn) (PeerCreds, bool) {
	_ = conn
	return PeerCreds{}, false
}
