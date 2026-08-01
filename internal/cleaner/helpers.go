package cleaner

import (
	"os"
	"path/filepath"
	"time"
)

// GraceCutoffISO returns an ISO-8601 UTC timestamp: archives with removed_at
// at or before this value are past the cleanup_after grace period.
// Microseconds are truncated (second precision), matching state.UTCNowISO.
func GraceCutoffISO(cleanupAfterSeconds float64, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cutoff := now.UTC().Add(-time.Duration(cleanupAfterSeconds * float64(time.Second)))
	return cutoff.Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

// DirSizeBytes is a best-effort recursive size of a directory (no follow symlinks).
func DirSizeBytes(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

// freeBytesProbe returns free bytes on the filesystem containing path.
// Overridable in tests via SetFreeBytesFunc.
var freeBytesProbe = diskFreeBytes

// SetFreeBytesFunc replaces the free-space probe. Returns a restore function.
// For tests only.
func SetFreeBytesFunc(fn func(path string) (free int64, ok bool)) (restore func()) {
	prev := freeBytesProbe
	if fn == nil {
		freeBytesProbe = diskFreeBytes
	} else {
		freeBytesProbe = fn
	}
	return func() { freeBytesProbe = prev }
}

// FreeBytes returns free bytes for the filesystem containing path, or ok=false.
func FreeBytes(path string) (free int64, ok bool) {
	return freeBytesProbe(path)
}
