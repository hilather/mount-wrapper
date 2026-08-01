//go:build unix

package mounter

import (
	"os"
	"path/filepath"
	"syscall"
)

// DefaultIsMount reports whether path appears to be a mountpoint by comparing
// the device of path to its parent (classic os.path.ismount heuristic).
// Best-effort: returns false on any stat error.
func DefaultIsMount(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Lstat(path)
	if err != nil {
		return false
	}
	parent := filepath.Dir(path)
	if parent == path {
		// Root: treat as mount.
		return true
	}
	pst, err := os.Lstat(parent)
	if err != nil {
		return false
	}
	s1, ok1 := st.Sys().(*syscall.Stat_t)
	s2, ok2 := pst.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false
	}
	return s1.Dev != s2.Dev
}
