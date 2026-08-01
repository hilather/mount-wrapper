package cleaner

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// PathInUseFunc reports whether path is open by a running process.
// Production uses DefaultPathInUse (see path_in_use_*.go); nil keeps all.
type PathInUseFunc func(path string) bool

// IsRatarmountTempPath reports whether path is a ratarmount materialization
// temp file (regular file whose basename starts with ".tmp").
func IsRatarmountTempPath(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return strings.HasPrefix(filepath.Base(path), ".tmp")
}

// PruneOrphanRatarmountTemps removes unused ratarmount /tmp/.tmp* materialization
// files under tmpDir. Skips paths still open (inUse).
// Returns (removed_count, bytes_freed).
//
// Path safety: only removes direct children of tmpDir whose names start with ".tmp".
func PruneOrphanRatarmountTemps(tmpDir string, inUse PathInUseFunc) (removed int, freed int64) {
	if tmpDir == "" {
		return 0, 0
	}
	info, err := os.Stat(tmpDir)
	if err != nil || !info.IsDir() {
		return 0, 0
	}
	if inUse == nil {
		// Unknown — keep files rather than break a live mount.
		inUse = func(string) bool { return true }
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return 0, 0
	}
	// Sort by name for deterministic tests.
	// ReadDir order is already directory order; fine for parity.
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, ".tmp") {
			continue
		}
		candidate := filepath.Join(tmpDir, name)
		// Must stay a direct child under tmpDir.
		if !PathUnderRoot(candidate, tmpDir) {
			continue
		}
		if !IsRatarmountTempPath(candidate) {
			// Directories named .tmp* are not removed (parity: files only).
			continue
		}
		if inUse(candidate) {
			continue
		}
		var size int64
		if st, err := e.Info(); err == nil {
			size = st.Size()
		}
		if err := os.Remove(candidate); err != nil {
			slog.Warn("ratarmount temp cleanup failed", "path", candidate, "err", err)
			continue
		}
		removed++
		freed += size
		slog.Info("ratarmount temp removed", "path", candidate, "size", size)
	}
	return removed, freed
}
