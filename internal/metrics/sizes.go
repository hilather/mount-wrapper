package metrics

import (
	"os"
)

// SizeProvider resolves on-disk archive and index sizes.
// Tests inject fakes; production uses FSSizeProvider.
type SizeProvider interface {
	// FileSize returns the size of a regular file, or nil if missing/unreadable.
	FileSize(path string) *int64
	// IndexSize returns total size of the index file plus common sidecars
	// (.bak, -journal, -wal, -shm). Returns 0 if path is set but no files exist;
	// nil if path is empty.
	IndexSize(indexPath string) *int64
}

// FSSizeProvider implements SizeProvider using the local filesystem.
type FSSizeProvider struct{}

// FileSize returns st_size for a regular file, or nil if missing/unreadable/not a file.
func (FSSizeProvider) FileSize(path string) *int64 {
	return pathSizeBytes(path)
}

// IndexSize sums the index and common SQLite/ratarmount sidecar files.
func (FSSizeProvider) IndexSize(indexPath string) *int64 {
	return indexSizeBytes(indexPath)
}

// pathSizeBytes returns size of a file, or nil if missing/unreadable (parity path_size_bytes).
func pathSizeBytes(path string) *int64 {
	if path == "" {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !st.Mode().IsRegular() {
		return nil
	}
	v := st.Size()
	return &v
}

// indexSizeBytes returns total size of the index file and common sidecars.
// Returns 0 if path is set but no files exist; nil if indexPath is empty.
func indexSizeBytes(indexPath string) *int64 {
	if indexPath == "" {
		return nil
	}
	// Parity candidates: base, base.bak, base-journal, base-wal, base-shm
	candidates := []string{
		indexPath,
		indexPath + ".bak",
		indexPath + "-journal",
		indexPath + "-wal",
		indexPath + "-shm",
	}
	var total int64
	found := false
	for _, p := range candidates {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !st.Mode().IsRegular() {
			continue
		}
		total += st.Size()
		found = true
	}
	if !found {
		// Path set but nothing on disk yet → 0 (not nil).
		z := int64(0)
		return &z
	}
	return &total
}

// MapSizeProvider is a test SizeProvider backed by maps.
type MapSizeProvider struct {
	Files   map[string]int64 // path → size; missing key → nil
	Indexes map[string]int64 // index path → total size; missing → treat as 0 if path non-empty
	// MissingIndexAsNil when true makes IndexSize return nil for unknown paths
	// even when the path string is non-empty (default: return 0 like FS).
	MissingIndexAsNil bool
}

// FileSize implements SizeProvider.
func (m MapSizeProvider) FileSize(path string) *int64 {
	if path == "" || m.Files == nil {
		return nil
	}
	v, ok := m.Files[path]
	if !ok {
		return nil
	}
	return &v
}

// IndexSize implements SizeProvider.
func (m MapSizeProvider) IndexSize(indexPath string) *int64 {
	if indexPath == "" {
		return nil
	}
	if m.Indexes != nil {
		if v, ok := m.Indexes[indexPath]; ok {
			return &v
		}
	}
	if m.MissingIndexAsNil {
		return nil
	}
	z := int64(0)
	return &z
}

// IndexPresent implements IndexPresence: key present in Indexes.
func (m MapSizeProvider) IndexPresent(indexPath string) bool {
	if indexPath == "" || m.Indexes == nil {
		return false
	}
	_, ok := m.Indexes[indexPath]
	return ok
}
