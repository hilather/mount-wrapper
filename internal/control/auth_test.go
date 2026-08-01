package control_test

import (
	"net"
	"testing"

	"github.com/hilather/mount-wrapper/internal/control"
	"github.com/hilather/mount-wrapper/internal/platform"
)

func TestAuthorizePeerAllowAll(t *testing.T) {
	t.Parallel()
	r := control.AuthorizePeer(nil, control.AuthorizeOpts{AllowAll: true})
	if !r.Allowed || r.Reason != "auth_disabled" {
		t.Fatalf("%+v", r)
	}
}

func TestAuthorizePeerRoot(t *testing.T) {
	t.Parallel()
	r := control.AuthorizePeer(nil, control.AuthorizeOpts{
		PeerCredentials: func(net.Conn) (platform.PeerCreds, bool) {
			return platform.PeerCreds{PID: 1, UID: 0, GID: 0}, true
		},
	})
	if !r.Allowed || r.Reason != "root" {
		t.Fatalf("%+v", r)
	}
}

func TestAuthorizePeerGroupMember(t *testing.T) {
	t.Parallel()
	r := control.AuthorizePeer(nil, control.AuthorizeOpts{
		GroupName: "mount-wrapper",
		PeerCredentials: func(net.Conn) (platform.PeerCreds, bool) {
			return platform.PeerCreds{PID: -1, UID: 1000, GID: 1000}, true
		},
		UserInGroup: func(uid int, groupName string) bool {
			return uid == 1000 && groupName == "mount-wrapper"
		},
	})
	if !r.Allowed || r.Reason != "group:mount-wrapper" {
		t.Fatalf("%+v", r)
	}
}

func TestAuthorizePeerDeny(t *testing.T) {
	t.Parallel()
	r := control.AuthorizePeer(nil, control.AuthorizeOpts{
		GroupName: "mount-wrapper",
		PeerCredentials: func(net.Conn) (platform.PeerCreds, bool) {
			return platform.PeerCreds{PID: 42, UID: 1234, GID: 1234}, true
		},
		UserInGroup: func(int, string) bool { return false },
	})
	if r.Allowed {
		t.Fatalf("expected deny: %+v", r)
	}
	if r.Reason == "" {
		t.Fatal("empty reason")
	}
}

func TestAuthorizePeerNoCredsDeny(t *testing.T) {
	t.Parallel()
	falseVal := false
	r := control.AuthorizePeer(nil, control.AuthorizeOpts{
		PeerCredentials: func(net.Conn) (platform.PeerCreds, bool) {
			return platform.PeerCreds{}, false
		},
		AllowUnauthEnv: &falseVal,
	})
	if r.Allowed {
		t.Fatalf("expected deny: %+v", r)
	}
	if r.Reason != "peer credentials unavailable" {
		t.Fatalf("reason=%s", r.Reason)
	}
}

func TestAuthorizePeerNoCredsAllowEnv(t *testing.T) {
	t.Parallel()
	trueVal := true
	r := control.AuthorizePeer(nil, control.AuthorizeOpts{
		PeerCredentials: func(net.Conn) (platform.PeerCreds, bool) {
			return platform.PeerCreds{}, false
		},
		AllowUnauthEnv: &trueVal,
	})
	if !r.Allowed {
		t.Fatalf("expected allow: %+v", r)
	}
	if r.Reason != platform.ControlAllowUnauthEnv {
		t.Fatalf("reason=%s", r.Reason)
	}
}

func TestAuthorizePeerDarwinPIDUnknownStillAuthsByUID(t *testing.T) {
	t.Parallel()
	// Darwin LOCAL_PEERCRED: pid=-1; auth must still succeed via group/root.
	r := control.AuthorizePeer(nil, control.AuthorizeOpts{
		PeerCredentials: func(net.Conn) (platform.PeerCreds, bool) {
			return platform.PeerCreds{PID: -1, UID: 0, GID: 0}, true
		},
	})
	if !r.Allowed {
		t.Fatalf("%+v", r)
	}
}
