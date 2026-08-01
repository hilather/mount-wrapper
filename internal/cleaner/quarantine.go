package cleaner

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// PruneQuarantine deletes quarantine entries older than retainFor and/or over
// maxBytes (oldest first when maxBytes > 0).
//
// Entries must live under overlayDir/.quarantine (path safety).
// Returns (entries_removed, bytes_freed).
func PruneQuarantine(overlayDir string, retainFor time.Duration, maxBytes int64, now time.Time) (removed int, freed int64) {
	qroot := QuarantineDir(overlayDir)
	if !PathUnderRoot(qroot, overlayDir) {
		// Should never happen for normal paths; refuse anyway.
		return 0, 0
	}
	info, err := os.Stat(qroot)
	if err != nil || !info.IsDir() {
		return 0, 0
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	entries, err := os.ReadDir(qroot)
	if err != nil {
		slog.Warn("cannot list quarantine", "err", err)
		return 0, 0
	}

	type qEntry struct {
		path  string
		mtime time.Time
		size  int64
	}
	var survivors []qEntry

	for _, e := range entries {
		child := filepath.Join(qroot, e.Name())
		if !PathUnderRoot(child, qroot) {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		var size int64
		if st.IsDir() {
			size = DirSizeBytes(child)
		} else {
			size = st.Size()
		}
		mtime := st.ModTime()
		if now.Sub(mtime) >= retainFor {
			if err := removeQuarantineEntry(child, st); err != nil {
				slog.Warn("quarantine prune failed", "path", child, "err", err)
				continue
			}
			removed++
			freed += size
			slog.Info("pruned quarantine entry (age)", "path", child)
			continue
		}
		survivors = append(survivors, qEntry{path: child, mtime: mtime, size: size})
	}

	if maxBytes > 0 {
		var total int64
		for _, e := range survivors {
			total += e.size
		}
		sort.Slice(survivors, func(i, j int) bool {
			return survivors[i].mtime.Before(survivors[j].mtime)
		})
		for _, e := range survivors {
			if total <= maxBytes {
				break
			}
			st, err := os.Lstat(e.path)
			if err != nil {
				continue
			}
			if err := removeQuarantineEntry(e.path, st); err != nil {
				slog.Warn("quarantine size prune failed", "path", e.path, "err", err)
				continue
			}
			removed++
			freed += e.size
			total -= e.size
			slog.Info("pruned quarantine entry (size cap)", "path", e.path)
		}
	}
	return removed, freed
}

func removeQuarantineEntry(path string, st os.FileInfo) error {
	if st.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}
