package control

import (
	"fmt"
	"net"
	"os/user"
	"strconv"

	"github.com/hilather/mount-wrapper/internal/platform"
)

// DefaultAuthGroup is the Unix group allowed to use the control socket
// (service group; D9).
const DefaultAuthGroup = "mount-wrapper"

// DefaultServiceUser is the dedicated service account used for best-effort
// socket ownership (D9).
const DefaultServiceUser = "mount-wrapper"

// PeerCredentialsFunc obtains peer credentials for a connected Unix socket.
// Default is platform.PeerCredentials. Tests inject fakes.
type PeerCredentialsFunc func(conn net.Conn) (platform.PeerCreds, bool)

// UserInGroupFunc reports whether uid is root or a member of groupName.
// Default is UserInGroup. Tests inject fakes.
type UserInGroupFunc func(uid int, groupName string) bool

// UserInGroup returns true if uid is 0 (root) or a member of groupName
// (primary GID or supplementary groups via os/user).
func UserInGroup(uid int, groupName string) bool {
	if uid == 0 {
		return true
	}
	if groupName == "" {
		return false
	}
	g, err := user.LookupGroup(groupName)
	if err != nil {
		return false
	}
	wantGID := g.Gid

	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return false
	}
	if u.Gid == wantGID {
		return true
	}
	// Supplementary groups (includes primary on most platforms).
	ids, err := u.GroupIds()
	if err != nil {
		return false
	}
	for _, id := range ids {
		if id == wantGID {
			return true
		}
	}
	return false
}

// AuthResult is the outcome of AuthorizePeer.
type AuthResult struct {
	Allowed bool
	Reason  string
}

// AuthorizeOpts configures peer authorization.
type AuthorizeOpts struct {
	AllowAll  bool
	GroupName string
	// PeerCredentials overrides platform.PeerCredentials when non-nil.
	PeerCredentials PeerCredentialsFunc
	// UserInGroup overrides UserInGroup when non-nil.
	UserInGroup UserInGroupFunc
	// AllowUnauthEnv, when true, allows connections when peercred is
	// unavailable (operator escape hatch). Defaults to platform.ControlAllowUnauth.
	// Use a pointer so callers can force false even when the env is set (tests).
	AllowUnauthEnv *bool
}

// AuthorizePeer authorizes a connected peer. Returns (allowed, reason).
//
// Policy:
//  1. AllowAll → allow ("auth_disabled")
//  2. Peer credentials unavailable → allow only if MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH=1
//  3. uid 0 → allow ("root")
//  4. membership in GroupName (default mount-wrapper) → allow
//  5. else deny
//
// Darwin may report pid=-1; auth still uses uid/group.
func AuthorizePeer(conn net.Conn, opts AuthorizeOpts) AuthResult {
	if opts.AllowAll {
		return AuthResult{Allowed: true, Reason: "auth_disabled"}
	}
	group := opts.GroupName
	if group == "" {
		group = DefaultAuthGroup
	}
	peerFn := opts.PeerCredentials
	if peerFn == nil {
		peerFn = platform.PeerCredentials
	}
	creds, ok := peerFn(conn)
	if !ok {
		allowEnv := platform.ControlAllowUnauth()
		if opts.AllowUnauthEnv != nil {
			allowEnv = *opts.AllowUnauthEnv
		}
		if allowEnv {
			return AuthResult{Allowed: true, Reason: platform.ControlAllowUnauthEnv}
		}
		return AuthResult{Allowed: false, Reason: "peer credentials unavailable"}
	}
	if creds.UID == 0 {
		return AuthResult{Allowed: true, Reason: "root"}
	}
	inGroup := UserInGroup
	if opts.UserInGroup != nil {
		inGroup = opts.UserInGroup
	}
	if inGroup(creds.UID, group) {
		return AuthResult{Allowed: true, Reason: "group:" + group}
	}
	return AuthResult{
		Allowed: false,
		Reason:  fmt.Sprintf("uid %d is not root or in group %s", creds.UID, group),
	}
}
