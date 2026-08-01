package cleaner

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hilather/mount-wrapper/internal/convert"
)

// NonsolidCachePruneResult summarizes one outer nonsolid cache hygiene pass.
type NonsolidCachePruneResult struct {
	// PartialsRemoved: *.nonsolid.partial files and *.nonsolid.partial.work dirs.
	PartialsRemoved int
	// LocksRemoved: stale *.lock files whose sibling .7z is missing.
	LocksRemoved int
	// ArchivesRemoved: age-pruned orphaned *.7z (and their sidecars).
	ArchivesRemoved int
	// SidecarsRemoved: *.tarmount-convert.json removed with archives (or alone).
	SidecarsRemoved int
	// BytesFreed best-effort sum of removed file/dir sizes.
	BytesFreed int64
}

// PruneNonsolidCache performs disk hygiene under the outer nonsolid 7z cache
// directory (config convert_7z_cache_dir or DefaultNonsolidCacheDir).
//
// Policy (reuses cleanup_after / CleanupAfterSeconds; no separate config key):
//  1. Always remove leftover *.nonsolid.partial files and
//     *.nonsolid.partial.work directories (crashed populate residue).
//  2. Remove stale *.lock only when the corresponding *.7z is missing
//     (best-effort; skip when the lock is held by a live populate).
//  3. Age-prune orphaned *.7z whose mtime is older than cleanupAfter, plus
//     sibling *.tarmount-convert.json and *.lock. Paths listed in livePaths
//     (e.g. LivePaths mount targets) are skipped.
//
// Path safety: only direct children of cacheDir are considered; every delete
// is refused unless PathUnderRoot(candidate, cacheDir). Missing or non-dir
// cacheDir is a no-op.
func PruneNonsolidCache(
	cacheDir string,
	cleanupAfter time.Duration,
	now time.Time,
	livePaths []string,
) NonsolidCachePruneResult {
	var result NonsolidCachePruneResult
	cacheDir = strings.TrimSpace(cacheDir)
	if cacheDir == "" {
		return result
	}
	absCache, err := filepath.Abs(cacheDir)
	if err != nil {
		return result
	}
	absCache = filepath.Clean(absCache)
	info, err := os.Stat(absCache)
	if err != nil || !info.IsDir() {
		return result
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	live := make(map[string]struct{}, len(livePaths))
	for _, p := range livePaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if abs, err := filepath.Abs(p); err == nil {
			live[filepath.Clean(abs)] = struct{}{}
		} else {
			live[filepath.Clean(p)] = struct{}{}
		}
	}

	entries, err := os.ReadDir(absCache)
	if err != nil {
		slog.Warn("nonsolid cache list failed", "dir", absCache, "err", err)
		return result
	}

	// Pass 1: leftover partials and work dirs (always).
	for _, e := range entries {
		name := e.Name()
		candidate := filepath.Join(absCache, name)
		if !PathUnderRoot(candidate, absCache) {
			continue
		}
		switch {
		case strings.HasSuffix(name, convert.NonsolidPartialWorkSuffix):
			// Directory (or stray file) from extract work.
			var size int64
			if st, err := e.Info(); err == nil {
				if st.IsDir() {
					size = DirSizeBytes(candidate)
				} else {
					size = st.Size()
				}
			}
			if err := os.RemoveAll(candidate); err != nil {
				slog.Warn("nonsolid cache partial work remove failed", "path", candidate, "err", err)
				continue
			}
			result.PartialsRemoved++
			result.BytesFreed += size
			slog.Info("nonsolid cache partial work removed", "path", candidate, "size", size)
		case strings.HasSuffix(name, convert.NonsolidPartialSuffix):
			// Regular partial file (not the .work sibling — that ends with .work).
			if e.IsDir() {
				// Unexpected dir named *.nonsolid.partial — still removeAll under root.
				size := DirSizeBytes(candidate)
				if err := os.RemoveAll(candidate); err != nil {
					slog.Warn("nonsolid cache partial dir remove failed", "path", candidate, "err", err)
					continue
				}
				result.PartialsRemoved++
				result.BytesFreed += size
				continue
			}
			var size int64
			if st, err := e.Info(); err == nil {
				size = st.Size()
			}
			if err := os.Remove(candidate); err != nil {
				slog.Warn("nonsolid cache partial remove failed", "path", candidate, "err", err)
				continue
			}
			result.PartialsRemoved++
			result.BytesFreed += size
			slog.Info("nonsolid cache partial removed", "path", candidate, "size", size)
		}
	}

	// Re-list after partial cleanup so subsequent passes see a consistent set.
	entries, err = os.ReadDir(absCache)
	if err != nil {
		return result
	}

	// Pass 2: stale locks when sibling .7z is missing.
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".lock") {
			continue
		}
		lockPath := filepath.Join(absCache, name)
		if !PathUnderRoot(lockPath, absCache) {
			continue
		}
		dest := convert.NonsolidCacheDestFromLockPath(lockPath)
		if dest == "" || !PathUnderRoot(dest, absCache) {
			continue
		}
		if fileExistsRegularOrAny(dest) {
			continue // live or completed cache entry — keep lock
		}
		var size int64
		if st, err := e.Info(); err == nil {
			size = st.Size()
		}
		if !tryRemoveLockFile(lockPath) {
			// Busy or remove failed — ignore (best-effort).
			continue
		}
		result.LocksRemoved++
		result.BytesFreed += size
		slog.Info("nonsolid cache stale lock removed", "path", lockPath)
	}

	// Pass 3: age-prune orphaned *.7z (+ sidecar + lock).
	if cleanupAfter < 0 {
		return result
	}
	entries, err = os.ReadDir(absCache)
	if err != nil {
		return result
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".7z") {
			continue
		}
		// Skip partial-looking names (already handled; belt-and-suspenders).
		if strings.Contains(name, convert.NonsolidPartialSuffix) {
			continue
		}
		dest := filepath.Join(absCache, name)
		if !PathUnderRoot(dest, absCache) {
			continue
		}
		if _, skip := live[dest]; skip {
			continue
		}
		st, err := e.Info()
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		if now.Sub(st.ModTime()) < cleanupAfter {
			continue
		}
		size := st.Size()
		if err := os.Remove(dest); err != nil {
			slog.Warn("nonsolid cache archive remove failed", "path", dest, "err", err)
			continue
		}
		result.ArchivesRemoved++
		result.BytesFreed += size
		slog.Info("nonsolid cache orphan pruned", "path", dest, "age", now.Sub(st.ModTime()).String())

		// Sibling convert metadata sidecar.
		meta := convert.MetadataPath(dest)
		if PathUnderRoot(meta, absCache) {
			if mst, err := os.Lstat(meta); err == nil && mst.Mode().IsRegular() {
				msz := mst.Size()
				if err := os.Remove(meta); err == nil {
					result.SidecarsRemoved++
					result.BytesFreed += msz
				}
			}
		}
		// Sibling lock (best-effort; ignore if busy).
		lockPath := convert.NonsolidCacheLockPath(dest)
		if PathUnderRoot(lockPath, absCache) {
			if lst, err := os.Lstat(lockPath); err == nil && lst.Mode().IsRegular() {
				lsz := lst.Size()
				if tryRemoveLockFile(lockPath) {
					result.LocksRemoved++
					result.BytesFreed += lsz
				}
			}
		}
	}

	return result
}

func fileExistsRegularOrAny(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
