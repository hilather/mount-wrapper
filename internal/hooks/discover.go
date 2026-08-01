package hooks

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Filename suffixes that are never treated as hooks.
var ignoreSuffixes = []string{
	".sample",
	".disabled",
	".dpkg-new",
	".dpkg-dist",
	".rpmnew",
	".rpmsave",
}

// Exact basenames ignored in hooks.d (case-sensitive set + README* prefix rule).
var ignoreNames = map[string]struct{}{
	"README":    {},
	"README.md": {},
	"readme":    {},
	"readme.md": {},
	"LICENSE":   {},
	"NOTICE":    {},
}

// IsIgnoredHookName reports whether name should not be treated as a hook script.
func IsIgnoredHookName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return true
	}
	if _, ok := ignoreNames[name]; ok {
		return true
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "readme") {
		return true
	}
	for _, suffix := range ignoreSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// DiscoverHooks lists executable regular files (and symlinks to files) in
// hooksDir, sorted by basename. Ignores samples, disabled, README*, non-files.
// Does not apply security policy (use ValidateHookSecurity).
func DiscoverHooks(hooksDir string) []DiscoveredHook {
	root := hooksDir
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		slog.Warn("cannot list hooks_dir", "path", root, "err", err)
		return nil
	}

	// Sort by name (ReadDir is sorted on some systems; sort explicitly for parity).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var found []DiscoveredHook
	for _, ent := range entries {
		name := ent.Name()
		if IsIgnoredHookName(name) {
			continue
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		mode := info.Mode()
		if mode&fs.ModeSymlink != 0 {
			// Symlinks to files are validated later; must resolve to a file.
			targetInfo, err := os.Stat(path)
			if err != nil || !targetInfo.Mode().IsRegular() {
				continue
			}
		} else if !mode.IsRegular() {
			continue
		}
		// Must be executable by someone (owner/group/other exec bit).
		// Use Lstat mode for the entry itself (symlink mode is typically 0777).
		if mode&0o111 == 0 {
			// For symlinks, also accept if target is executable.
			if mode&fs.ModeSymlink != 0 {
				targetInfo, err := os.Stat(path)
				if err != nil || targetInfo.Mode()&0o111 == 0 {
					slog.Debug("skip non-executable hook candidate", "path", path)
					continue
				}
			} else {
				slog.Debug("skip non-executable hook candidate", "path", path)
				continue
			}
		}
		found = append(found, DiscoveredHook{Name: name, Path: path})
	}
	return found
}
