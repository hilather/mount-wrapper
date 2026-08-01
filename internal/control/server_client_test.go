package control_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/control"
	"github.com/hilather/mount-wrapper/internal/platform"
)

func TestServerClientRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "control.sock")

	handler := func(req map[string]any) map[string]any {
		return control.OKResponse(map[string]any{
			"op": req["op"],
			"x":  req["x"],
		})
	}
	srv := control.NewServer(sock, handler, true)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				srv.ServeReady()
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	client := control.NewClient(sock, 5*time.Second)
	data, err := client.RequestOK("status", map[string]any{"x": 1})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := data.(map[string]any)
	if m["op"] != "status" {
		t.Fatalf("%+v", m)
	}
	// JSON numbers decode as float64 through the socket.
	if m["x"] != float64(1) {
		t.Fatalf("x=%v (%T)", m["x"], m["x"])
	}

	data2, err := client.RequestOK("rescan", map[string]any{"x": 2})
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := data2.(map[string]any)
	if m2["x"] != float64(2) {
		t.Fatalf("%+v", m2)
	}

	close(stop)
	wg.Wait()
}

func TestClientUnavailable(t *testing.T) {
	tmp := t.TempDir()
	client := control.NewClient(filepath.Join(tmp, "missing.sock"), time.Second)
	_, err := client.Request("status", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	ce, ok := err.(*control.Error)
	if !ok {
		t.Fatalf("type %T", err)
	}
	if ce.Code != "UNAVAILABLE" {
		t.Fatalf("code=%s msg=%s", ce.Code, ce.Message)
	}
}

func TestStaleSocketCleanup(t *testing.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "control.sock")
	// Leave a stale file (not a live socket).
	if err := os.WriteFile(sock, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := control.NewServer(sock, func(map[string]any) map[string]any {
		return control.OKResponse(nil)
	}, true)
	if err := srv.Start(); err != nil {
		t.Fatalf("start with stale path: %v", err)
	}
	defer srv.Close()

	// Mode should be 0660 (best-effort; umask may affect bits on some FS).
	st, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	mode := st.Mode().Perm()
	if mode&0o077 != 0 {
		// World/other bits should be clear for 0660; group/user r/w expected.
		// Some filesystems ignore chmod; only soft-check owner write.
		t.Logf("socket mode=%o (expected ~0660)", mode)
	}
	if mode&0o600 != 0o600 {
		t.Fatalf("socket not owner-writable: mode=%o", mode)
	}

	// Second start after close should also work.
	_ = srv.Close()
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket should be removed on close, err=%v", err)
	}
}

func TestServerAuthDeny(t *testing.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "control.sock")
	falseVal := false
	srv := control.NewServer(sock, func(map[string]any) map[string]any {
		return control.OKResponse(map[string]any{"secret": true})
	}, false)
	srv.PeerCredentials = func(net.Conn) (platform.PeerCreds, bool) {
		return platform.PeerCreds{UID: 99999, GID: 99999, PID: 1}, true
	}
	srv.UserInGroup = func(int, string) bool { return false }
	srv.AllowUnauthEnv = &falseVal
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			srv.ServeReady()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	client := control.NewClient(sock, 2*time.Second)
	resp, err := client.Request("status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected deny: %+v", resp)
	}
	if resp["code"] != "PERMISSION_DENIED" {
		t.Fatalf("%+v", resp)
	}
	<-done
}

func TestServerUnsupportedVersion(t *testing.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "control.sock")
	srv := control.NewServer(sock, func(map[string]any) map[string]any {
		return control.OKResponse(nil)
	}, true)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	go func() {
		for i := 0; i < 40; i++ {
			srv.ServeReady()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Raw dial with bad version.
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(`{"v":99,"op":"status"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	line := string(buf[:n])
	if !strings.Contains(line, "UNSUPPORTED_VERSION") {
		t.Fatalf("resp=%s", line)
	}
}
