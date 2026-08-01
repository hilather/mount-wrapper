//go:build unix

package convert

import (
	"os"
	"syscall"
)

// flockExclusive takes a blocking exclusive flock (LOCK_EX).
// Used for outer nonsolid cache populate critical sections (parity with
// Python fcntl.flock LOCK_EX on {cacheKey}.lock).
func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func flockUnlock(f *os.File) error {
	if f == nil {
		return nil
	}
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
