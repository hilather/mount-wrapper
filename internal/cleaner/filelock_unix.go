//go:build unix

package cleaner

import (
	"os"
	"syscall"
)

// tryRemoveLockFile removes path only after taking a non-blocking exclusive
// flock. If another process holds the lock (live outer-cache populate), the
// remove is skipped. Returns true when the file was removed.
func tryRemoveLockFile(path string) bool {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		// Already gone or unreadable — treat as not removed by us.
		return false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return false
	}
	// Hold lock across unlink so a concurrent populate cannot open a new
	// inode under the same name while we delete.
	err = os.Remove(path)
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
	return err == nil
}
