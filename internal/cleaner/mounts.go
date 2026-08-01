package cleaner

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Mount-dir statuses that must not be auto-removed even when not ismount yet.
var mountDirProtectedStatuses = map[string]struct{}{
	state.StatusIndexing:     {},
	state.StatusMounting:     {},
	state.StatusMounted:      {},
	state.StatusHooksRunning: {},
}

// IsMountFunc reports whether path is an active mountpoint.
type IsMountFunc func(path string) bool

// RemoveUnusedMountDir removes path when it is not an active FUSE mount.
// Returns true when the path was removed.
//
// When root is non-empty, path must resolve under root (path safety).
func RemoveUnusedMountDir(path string, isMount IsMountFunc, root string) bool {
	if path == "" {
		return false
	}
	if root != "" && !PathUnderRoot(path, root) {
		slog.Warn("refusing to remove mount dir outside mount_root", "path", path, "mount_root", root)
		return false
	}
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if isMount == nil {
		isMount = func(string) bool { return false }
	}
	if isMount(path) {
		slog.Warn("refusing to remove live mount point", "path", path)
		return false
	}
	if info.IsDir() {
		if err := os.Remove(path); err == nil {
			slog.Info("mount dir removed", "path", path)
			return true
		}
		// Non-empty: rmtree (parity with Python fallback).
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("failed to remove stale mount dir", "path", path, "err", err)
			return false
		}
		slog.Info("mount dir removed", "path", path)
		return true
	}
	if err := os.Remove(path); err != nil {
		return false
	}
	slog.Info("mount dir removed", "path", path)
	return true
}

// CleanupMountPoint removes an unused mount directory if present and not a live mount.
func CleanupMountPoint(mountPath string, isMount IsMountFunc, mountRoot string) bool {
	if mountPath == "" {
		return false
	}
	return RemoveUnusedMountDir(mountPath, isMount, mountRoot)
}

// reservedMountRootNames returns directory names under mount_root that must
// never be auto-removed (e.g. archives_dir nested under mount_root).
func reservedMountRootNames(cfg *config.Config) map[string]struct{} {
	names := map[string]struct{}{}
	if cfg == nil || strings.TrimSpace(cfg.ArchivesDir) == "" {
		return names
	}
	mountRoot, err := filepath.Abs(cfg.MountRoot)
	if err != nil {
		return names
	}
	mountRoot = filepath.Clean(mountRoot)
	archives, err := filepath.Abs(cfg.ArchivesDir)
	if err != nil {
		return names
	}
	archives = filepath.Clean(archives)
	if archives == mountRoot {
		return names
	}
	// archives under mount_root → protect the first child component.
	prefix := mountRoot + string(filepath.Separator)
	if !strings.HasPrefix(archives, prefix) {
		return names
	}
	rel, err := filepath.Rel(mountRoot, archives)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return names
	}
	// Protect the top-level name under mount_root.
	top := strings.Split(rel, string(filepath.Separator))[0]
	if top != "" {
		names[top] = struct{}{}
	}
	return names
}

// protectedMountPaths returns mount paths that must be kept (active statuses + live).
func protectedMountPaths(store *state.Store, liveMountPaths []string) map[string]struct{} {
	protected := map[string]struct{}{}
	if store != nil {
		recs, err := store.ListArchives(nil)
		if err == nil {
			for _, rec := range recs {
				if rec == nil || rec.MountPath == nil || *rec.MountPath == "" {
					continue
				}
				if _, ok := mountDirProtectedStatuses[rec.Status]; !ok {
					continue
				}
				if abs, err := filepath.Abs(*rec.MountPath); err == nil {
					protected[filepath.Clean(abs)] = struct{}{}
				} else {
					protected[filepath.Clean(*rec.MountPath)] = struct{}{}
				}
			}
		}
	}
	for _, p := range liveMountPaths {
		if p == "" {
			continue
		}
		if abs, err := filepath.Abs(p); err == nil {
			protected[filepath.Clean(abs)] = struct{}{}
		} else {
			protected[filepath.Clean(p)] = struct{}{}
		}
	}
	return protected
}

// CleanupStaleMountDirs removes unused directories under mount_root that are not
// live mounts and not protected by active archive status.
func CleanupStaleMountDirs(
	cfg *config.Config,
	store *state.Store,
	isMount IsMountFunc,
	liveMountPaths []string,
) []string {
	if cfg == nil || strings.TrimSpace(cfg.MountRoot) == "" {
		return nil
	}
	root := cfg.MountRoot
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	if isMount == nil {
		isMount = func(string) bool { return false }
	}
	reserved := reservedMountRootNames(cfg)
	protected := protectedMountPaths(store, liveMountPaths)

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if _, ok := reserved[name]; ok {
			continue
		}
		child := filepath.Join(root, name)
		if isMount(child) {
			continue
		}
		resolved := child
		if abs, err := filepath.Abs(child); err == nil {
			resolved = filepath.Clean(abs)
		}
		if _, ok := protected[resolved]; ok {
			continue
		}
		if RemoveUnusedMountDir(child, isMount, root) {
			removed = append(removed, child)
		}
	}
	if removed == nil {
		removed = []string{}
	}
	return removed
}
