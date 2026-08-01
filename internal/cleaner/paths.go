package cleaner

import (
	"path/filepath"
	"strings"
)

// PathUnderRoot reports whether path resolves under root (or equals root).
// Both paths are cleaned/abs'd best-effort. Empty path or root returns false.
// Does not follow the final path component as a symlink for the prefix check
// beyond filepath.Abs; refuse is safer than delete when resolution is ambiguous.
func PathUnderRoot(path, root string) bool {
	path = strings.TrimSpace(path)
	root = strings.TrimSpace(root)
	if path == "" || root == "" {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath = filepath.Clean(absPath)
	absRoot = filepath.Clean(absRoot)
	if absPath == absRoot {
		return true
	}
	prefix := absRoot + string(filepath.Separator)
	return strings.HasPrefix(absPath, prefix)
}

// QuarantineDir returns overlay_dir/.quarantine.
func QuarantineDir(overlayDir string) string {
	return filepath.Join(overlayDir, ".quarantine")
}
