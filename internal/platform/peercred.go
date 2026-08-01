package platform

import (
	"net"
	"os"
)

// ControlAllowUnauthEnv is the environment variable that, when set to "1",
// allows control-plane connections without peer credentials.
//
// This is an optional escape hatch for broken peercred (e.g. some Darwin edge
// cases). Prefer fixing peer credentials; do not enable in production unless
// necessary. Name is MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH (not TARMOUNT_*).
const ControlAllowUnauthEnv = "MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH"

// ControlAllowUnauth reports whether MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH is "1".
func ControlAllowUnauth() bool {
	return os.Getenv(ControlAllowUnauthEnv) == "1"
}

// PeerCreds holds Unix peer credentials for a connected control-socket peer.
//
// PID may be -1 when the platform cannot report it (Darwin LOCAL_PEERCRED /
// xucred). Callers must treat PID == -1 as unknown.
type PeerCreds struct {
	PID int
	UID int
	GID int
}

// PeerCredentials returns peer credentials for a connected Unix socket peer.
// ok is false when credentials cannot be obtained.
//
//   - Linux: SO_PEERCRED via golang.org/x/sys/unix (GetsockoptUcred) — pid, uid, gid.
//   - Darwin: LOCAL_PEERCRED / xucred (GetsockoptXucred) — uid (+ groups); pid = -1.
//   - Other: not available (ok=false).
//
// See also ControlAllowUnauth for the operator escape hatch when peercred fails.
func PeerCredentials(conn net.Conn) (PeerCreds, bool) {
	return peerCredentials(conn)
}

// PeercredBackendLabel is a human label for doctor / logs describing which
// mechanism is used on platform (or the host when platform is empty).
func PeercredBackendLabel(platform string) string {
	if platform == "" {
		platform = HostPlatform()
	}
	if HostPlatformOf(platform) == PlatformDarwin {
		// Go uses GetsockoptXucred (LOCAL_PEERCRED); Python prefers getpeereid
		// when present. PID is always unknown (-1) on this path.
		return "LOCAL_PEERCRED (best-effort)"
	}
	return "SO_PEERCRED"
}
