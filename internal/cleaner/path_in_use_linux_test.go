//go:build linux

package cleaner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hilather/mount-wrapper/internal/cleaner"
)

func TestDefaultPathInUseOpenFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, ".tmpopen")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Closed file: not in use (no process holds an open fd).
	if cleaner.DefaultPathInUse(path) {
		t.Fatal("closed file should not report in-use")
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if !cleaner.DefaultPathInUse(path) {
		t.Fatal("open file should report in-use via /proc/*/fd")
	}

	// Keep-open protects prune.
	removed, _ := cleaner.PruneOrphanRatarmountTemps(tmp, cleaner.DefaultPathInUse)
	if removed != 0 {
		t.Fatalf("open temp must not be pruned removed=%d", removed)
	}
	if _, err := f.Stat(); err != nil {
		t.Fatal("file vanished while still open")
	}

	_ = f.Close()
	// After close, prune may remove.
	removed, freed := cleaner.PruneOrphanRatarmountTemps(tmp, cleaner.DefaultPathInUse)
	if removed != 1 || freed != 4 {
		t.Fatalf("after close removed=%d freed=%d", removed, freed)
	}
}
