//go:build linux

package cleaner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultPathInUse reports whether path is open by any process via a Linux
// /proc/*/fd scan (parity with tarmount-wsl cleaner._linux_path_in_use).
//
// Returns false when the path cannot be resolved or /proc is unavailable
// (safe to treat as not in use only when the prune caller also checks
// existence; missing paths will not match open fds).
func DefaultPathInUse(path string) bool {
	return linuxPathInUse(path)
}

func linuxPathInUse(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	// Prefer resolved real path when the final component exists.
	resolved := absPath
	if r, err := filepath.EvalSymlinks(absPath); err == nil {
		resolved = r
	}

	proc, err := os.Open("/proc")
	if err != nil {
		return false
	}
	defer proc.Close()

	entries, err := proc.ReadDir(-1)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if _, err := strconv.Atoi(name); err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", name, "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if FdLinkMatchesPath(link, path, absPath) || FdLinkMatchesPath(link, path, resolved) {
				return true
			}
			// Resolve the link base and compare (symlink / relative targets).
			base := StripFdDeletedSuffix(link)
			if base == "" || !strings.HasPrefix(base, "/") {
				continue
			}
			if r, err := filepath.EvalSymlinks(base); err == nil {
				if r == resolved || r == absPath {
					return true
				}
			}
		}
	}
	return false
}
