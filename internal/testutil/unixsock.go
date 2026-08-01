// Package testutil holds small helpers shared by package tests.
package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ShortUnixSocketPath returns a Unix socket path short enough for macOS
// sockaddr_un / sun_path (~104 bytes). GitHub Actions t.TempDir() under
// /var/folders/... often exceeds that limit and causes Listen("unix", …)
// to fail with "invalid argument" / "filename too long".
//
// On non-darwin hosts the path is under t.TempDir() (auto-cleaned).
// On darwin it uses a short /tmp/mw-sock-* directory with t.Cleanup.
func ShortUnixSocketPath(t testing.TB, name string) string {
	t.Helper()
	if name == "" {
		name = "c.sock"
	}
	if runtime.GOOS != "darwin" {
		return filepath.Join(t.TempDir(), name)
	}
	dir, err := os.MkdirTemp("/tmp", "mw-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}
