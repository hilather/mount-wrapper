//go:build unix

package convert

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// diskFreeBytes returns free bytes available to a non-root user on the
// filesystem containing path (walking up to an existing ancestor).
func diskFreeBytes(path string) (free int64, ok bool) {
	target := path
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		target = filepath.Dir(target)
	}
	for {
		var st unix.Statfs_t
		if err := unix.Statfs(target, &st); err == nil {
			bsize := int64(st.Bsize)
			if bsize <= 0 {
				return 0, false
			}
			return int64(st.Bavail) * bsize, true
		}
		parent := filepath.Dir(target)
		if parent == target {
			return 0, false
		}
		target = parent
	}
}
