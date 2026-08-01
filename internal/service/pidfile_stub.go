//go:build !unix

package service

import "os"

// Non-Unix: no flock; exclusive is best-effort (second open still succeeds).
func flockExclusive(f *os.File) error {
	_ = f
	return nil
}

func unlockFile(f *os.File) error {
	_ = f
	return nil
}
