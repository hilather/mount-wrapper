package testutil_test

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/testutil"
)

func TestShortUnixSocketPathBindable(t *testing.T) {
	sock := testutil.ShortUnixSocketPath(t, "t.sock")
	if len(sock) > 100 {
		// macOS sun_path is ~104 including NUL; keep well under for CI margin.
		t.Fatalf("socket path too long (%d): %s", len(sock), sock)
	}
	if runtime.GOOS == "darwin" && !strings.HasPrefix(sock, "/tmp/") {
		t.Fatalf("darwin path should be under /tmp: %s", sock)
	}
	// Ensure parent exists and Listen succeeds (the point of the helper).
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("Listen unix %s: %v", sock, err)
	}
	_ = ln.Close()
	_ = os.Remove(sock)
}

func TestShortUnixSocketPathDefaultName(t *testing.T) {
	sock := testutil.ShortUnixSocketPath(t, "")
	if filepath.Base(sock) != "c.sock" {
		t.Fatalf("default name: %s", sock)
	}
}
