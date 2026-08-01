package mounter

import (
	"os"
	"strings"

	"github.com/hilather/mount-wrapper/internal/state"
)

// Partial-index cleanup statuses (never successfully mounted + index may be incomplete).
var partialIndexStatuses = map[string]struct{}{
	state.StatusIndexing:    {},
	state.StatusIndexFailed: {},
	state.StatusDiscovered:  {},
	state.StatusMountFailed: {},
}

// IndexFileReady reports whether the ratarmount sqlite index exists and is non-empty.
func IndexFileReady(indexPath string) bool {
	if strings.TrimSpace(indexPath) == "" {
		return false
	}
	info, err := os.Stat(indexPath)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Size() > 0
}

// ArchiveUsesInMemoryIndex is always false: ratarmount-rs writes on-disk indexes.
// Kept for API stability (was true only for the removed Python ratarmount 7z path).
func ArchiveUsesInMemoryIndex(archivePath, mountBackend string) bool {
	return false
}

// IndexBuildVerified reports when a --no-mount index pass is complete.
func IndexBuildVerified(indexPath, archivePath string, exitCode *int, mountBackend string) bool {
	if IndexFileReady(indexPath) {
		return true
	}
	return ArchiveUsesInMemoryIndex(archivePath, mountBackend) &&
		exitCode != nil && *exitCode == 0
}

// MountIndexRequirementMet reports when the mount phase may be treated as fully indexed.
func MountIndexRequirementMet(indexPath, archivePath, mountBackend string) bool {
	if IndexFileReady(indexPath) {
		return true
	}
	return ArchiveUsesInMemoryIndex(archivePath, mountBackend)
}

// NeedsFreshIndex reports when a full --no-mount index pass is required.
func NeedsFreshIndex(indexPath string) bool {
	return !IndexFileReady(indexPath)
}

// ForcedBackend returns an explicit --use-backend / -B value from extraArgs, if any.
func ForcedBackend(extraArgs []string) string {
	for i, arg := range extraArgs {
		if (arg == "--use-backend" || arg == "-B") && i+1 < len(extraArgs) {
			return strings.ToLower(extraArgs[i+1])
		}
		if strings.HasPrefix(arg, "--use-backend=") {
			return strings.ToLower(strings.TrimPrefix(arg, "--use-backend="))
		}
	}
	return ""
}

// UsesSinglePhaseMount is always false for ratarmount-rs (two-phase index+mount).
// Kept for API stability after removal of the Python sevenzip single-phase path.
func UsesSinglePhaseMount(archivePath string, extraArgs []string, mountBackend string, sevenzipAvailable bool) bool {
	return false
}

// ResolveNeedsIndex decides whether to run index-only before mount.
//
// A missing on-disk index always forces rebuild, even after first_mounted_at.
// firstIndex, when non-nil, forces index when true.
// mountBackend / sevenzipAvailable are retained for API stability (rust only).
func ResolveNeedsIndex(
	indexPath, archivePath string,
	firstIndex *bool,
	extraArgs []string,
	mountBackend string,
	sevenzipAvailable bool,
) bool {
	if NeedsFreshIndex(indexPath) {
		return true
	}
	if firstIndex != nil && *firstIndex {
		return true
	}
	return false
}

// ShouldDeletePartialIndex is the design partial-index rule:
// never successfully mounted + index path set + status in the partial set.
// Callers should also require that the index file exists before deleting.
func ShouldDeletePartialIndex(status string, firstMountedAt *string, indexPath string) bool {
	if firstMountedAt != nil && *firstMountedAt != "" {
		return false
	}
	if strings.TrimSpace(indexPath) == "" {
		return false
	}
	_, ok := partialIndexStatuses[status]
	return ok
}

// ShouldDeletePartialIndexFile combines ShouldDeletePartialIndex with existence.
func ShouldDeletePartialIndexFile(status string, firstMountedAt *string, indexPath string, indexIsFile bool) bool {
	return indexIsFile && ShouldDeletePartialIndex(status, firstMountedAt, indexPath)
}

// DeleteIndexFile deletes a (possibly partial) index file. Returns true if removed.
func DeleteIndexFile(indexPath string) bool {
	if strings.TrimSpace(indexPath) == "" {
		return false
	}
	info, err := os.Stat(indexPath)
	if err != nil || info.IsDir() {
		return false
	}
	if err := os.Remove(indexPath); err != nil {
		return false
	}
	return true
}

// ApplyPartialIndexCleanup deletes an incomplete index when rules match.
// Returns true if the file was deleted. Does not update state.Store (service layer).
// Empty index files still count as partial and are removed.
func ApplyPartialIndexCleanup(status string, firstMountedAt *string, indexPath string) bool {
	if !ShouldDeletePartialIndex(status, firstMountedAt, indexPath) {
		return false
	}
	return DeleteIndexFile(indexPath)
}
