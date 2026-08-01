package platform

import (
	"net"
	"os"
	"runtime"
	"testing"
)

func TestPeercredBackendLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		platform string
		want     string
	}{
		{"linux", "SO_PEERCRED"},
		{"other", "SO_PEERCRED"},
		{"windows", "SO_PEERCRED"},
		{"darwin", "LOCAL_PEERCRED (best-effort)"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.platform, func(t *testing.T) {
			t.Parallel()
			if got := PeercredBackendLabel(tc.platform); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestControlAllowUnauth(t *testing.T) {
	// Cannot t.Parallel with env.
	t.Setenv(ControlAllowUnauthEnv, "")
	if ControlAllowUnauth() {
		t.Fatal("empty env should be false")
	}
	t.Setenv(ControlAllowUnauthEnv, "0")
	if ControlAllowUnauth() {
		t.Fatal("0 should be false")
	}
	t.Setenv(ControlAllowUnauthEnv, "1")
	if !ControlAllowUnauth() {
		t.Fatal("1 should be true")
	}
	if ControlAllowUnauthEnv != "MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH" {
		t.Fatalf("env name leaked TARMOUNT or wrong: %s", ControlAllowUnauthEnv)
	}
}

func TestPeerCredentialsUnixSocketpair(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("peer credentials only on linux/darwin")
	}

	// Real Unix socket pair exercises the platform syscall path without FUSE.
	fds, err := socketpair()
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	defer fds[0].Close()
	defer fds[1].Close()

	creds, ok := PeerCredentials(fds[0])
	if !ok {
		t.Fatal("PeerCredentials failed on socketpair peer")
	}
	if creds.UID < 0 {
		t.Fatalf("uid=%d", creds.UID)
	}
	// On Linux, pid should be this process; on Darwin pid is unknown (-1).
	switch runtime.GOOS {
	case "linux":
		if creds.PID != os.Getpid() {
			// Socketpair peers are same process; SO_PEERCRED reports our pid.
			t.Fatalf("pid=%d want %d", creds.PID, os.Getpid())
		}
		if creds.UID != os.Getuid() {
			t.Fatalf("uid=%d want %d", creds.UID, os.Getuid())
		}
		if creds.GID != os.Getgid() {
			t.Fatalf("gid=%d want %d", creds.GID, os.Getgid())
		}
	case "darwin":
		if creds.PID != -1 {
			t.Fatalf("darwin pid should be -1 (unknown), got %d", creds.PID)
		}
		if creds.UID != os.Getuid() {
			t.Fatalf("uid=%d want %d", creds.UID, os.Getuid())
		}
	}
}

func TestPeerCredentialsRejectsNonUnix(t *testing.T) {
	// TCP connection is not a Unix conn → ok=false.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		// Keep open until client finishes.
		buf := make([]byte, 1)
		_, _ = c.Read(buf)
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, ok := PeerCredentials(client)
	if ok {
		t.Fatal("expected false for TCP conn")
	}
	_, _ = client.Write([]byte{0})
	<-done
}

// socketpair creates a connected Unix socket pair as net.Conn values.
func socketpair() ([2]net.Conn, error) {
	// net.Pipe is not a *UnixConn. Use a temporary path socket + accept/dial.
	// Prefer /tmp on darwin: default TempDir under /var/folders can exceed sun_path.
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "mw-peercred-")
	if err != nil {
		return [2]net.Conn{}, err
	}
	// Caller closes conns; remove path eagerly after accept.
	path := dir + "/s"
	ln, err := net.Listen("unix", path)
	if err != nil {
		os.RemoveAll(dir)
		return [2]net.Conn{}, err
	}

	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := ln.Accept()
		ch <- result{c, err}
	}()

	client, err := net.Dial("unix", path)
	if err != nil {
		ln.Close()
		os.RemoveAll(dir)
		return [2]net.Conn{}, err
	}
	res := <-ch
	ln.Close()
	os.RemoveAll(dir)
	if res.err != nil {
		client.Close()
		return [2]net.Conn{}, res.err
	}
	return [2]net.Conn{client, res.c}, nil
}
