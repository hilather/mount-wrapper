//go:build !unix

package convert

import "os"

// Non-Unix: no flock; exclusive is best-effort (second open still succeeds).
func flockExclusive(f *os.File) error {
	_ = f
	return nil
}

func flockUnlock(f *os.File) error {
	_ = f
	return nil
}
