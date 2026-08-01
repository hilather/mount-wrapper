package cleaner

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Unmounter unmounts a live archive during admin/grace purge.
// Implementations typically wrap mounter unmount + state transitions.
// Errors are logged; purge continues best-effort.
type Unmounter interface {
	Unmount(archiveID string) error
}

// UnmountFunc adapts a function to Unmounter.
type UnmountFunc func(archiveID string) error

// Unmount implements Unmounter.
func (f UnmountFunc) Unmount(archiveID string) error {
	if f == nil {
		return nil
	}
	return f(archiveID)
}

// LivePathsFunc returns paths currently held by a live registry (mount points
// and, when available, archive/cache paths used as mount sources).
type LivePathsFunc func() []string

// Cleaner performs grace-period purge and quarantine maintenance.
type Cleaner struct {
	Config *config.Config
	Store  *state.Store

	// Unmounter is optional; when set, PurgeArchive unmounts active statuses first.
	Unmounter Unmounter

	// LivePaths optionally supplies live mount paths for stale mount-dir cleanup.
	LivePaths LivePathsFunc

	// IsMount reports active mountpoints. Nil treats nothing as mounted (tests).
	IsMount IsMountFunc

	// Clock returns the current time (tests inject a fixed clock).
	Clock func() time.Time

	// TmpDir for orphan ratarmount temp prune. Empty uses "/tmp".
	// Tests may set a temp directory; production leave empty.
	TmpDir string

	// PathInUse for ratarmount temp prune. Nil keeps all (safe default for /tmp).
	// Tests inject a lambda; production may use Linux /proc scan (not implemented).
	PathInUse PathInUseFunc
}

// New constructs a Cleaner with time.Now as the clock.
func New(cfg *config.Config, store *state.Store) *Cleaner {
	return &Cleaner{
		Config: cfg,
		Store:  store,
		Clock:  time.Now,
	}
}

func (c *Cleaner) now() time.Time {
	if c != nil && c.Clock != nil {
		return c.Clock().UTC()
	}
	return time.Now().UTC()
}

// statuses that should attempt unmount before purge.
var purgeUnmountStatuses = map[string]struct{}{
	state.StatusMounted:      {},
	state.StatusHooksRunning: {},
	state.StatusIndexing:     {},
	state.StatusMounting:     {},
	state.StatusUnmounting:   {},
}

// PurgeArchive fully purges one archive: optional unmount, index delete,
// overlay policy, mount dir cleanup, then DELETE row.
//
// This is the admin / control-plane immediate purge API (require_yes is
// enforced by the control plane; this method always purges when called).
func (c *Cleaner) PurgeArchive(archiveID string, unmount bool) PurgeResult {
	result := PurgeResult{ArchiveID: archiveID, OverlayAction: OverlayNone}
	if c == nil || c.Store == nil || c.Config == nil {
		result.Error = "cleaner not configured"
		return result
	}

	rec, err := c.Store.GetArchive(archiveID)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if rec == nil {
		result.Error = "archive not found"
		return result
	}

	if unmount {
		if _, ok := purgeUnmountStatuses[rec.Status]; ok && c.Unmounter != nil {
			if err := c.Unmounter.Unmount(archiveID); err != nil {
				slog.Warn("purge unmount failed", "archive_id", archiveID, "err", err)
			}
			// Re-read after unmount.
			if rec2, err := c.Store.GetArchive(archiveID); err == nil && rec2 != nil {
				rec = rec2
			}
		}
	}

	// Index: only delete when under index_dir.
	if rec.IndexPath != nil && *rec.IndexPath != "" {
		idx := *rec.IndexPath
		if PathUnderRoot(idx, c.Config.IndexDir) {
			result.IndexDeleted = mounter.DeleteIndexFile(idx)
		} else {
			slog.Warn("refusing to delete index outside index_dir",
				"path", idx, "index_dir", c.Config.IndexDir)
		}
	}

	// Overlay policy.
	overlayPath := ""
	if rec.OverlayPath != nil {
		overlayPath = *rec.OverlayPath
	}
	action, dest, oerr := HandleOverlay(
		overlayPath,
		archiveID,
		c.Config.OverlayDir,
		c.Config.OverlayCleanup,
		c.now(),
	)
	result.OverlayAction = action
	result.OverlayDest = dest
	if oerr != nil {
		// Path refused or delete/move failure: record but still attempt DB purge
		// so rediscovery is not blocked by a stuck row. Artifacts outside roots
		// remain on disk for the operator.
		result.Error = oerr.Error()
		if action == OverlayRefused {
			slog.Warn("overlay policy refused", "archive_id", archiveID, "err", oerr)
		} else {
			slog.Error("overlay policy failed", "archive_id", archiveID, "err", oerr)
			// Fatal overlay failure (e.g. delete I/O): do not purge DB so operator
			// can retry. Path refused still purges (artifact intentionally left).
			if action != OverlayRefused {
				return result
			}
		}
	}

	// Mount directory cleanup.
	mountPath := ""
	if rec.MountPath != nil {
		mountPath = *rec.MountPath
	}
	result.MountCleaned = CleanupMountPoint(mountPath, c.IsMount, c.Config.MountRoot)

	if err := c.Store.PurgeArchive(archiveID); err != nil {
		if result.Error != "" {
			result.Error = result.Error + "; " + err.Error()
		} else {
			result.Error = err.Error()
		}
		slog.Error("purge DB failed", "archive_id", archiveID, "err", err)
		return result
	}

	result.OK = true
	// When overlay was OverlayRefused, Error may still describe the skipped
	// path so operators can clean it manually; OK remains true (row deleted).
	slog.Info("purged archive",
		"archive_id", archiveID,
		"index_deleted", result.IndexDeleted,
		"overlay", result.OverlayAction,
	)
	return result
}

// PurgeAbsentPastGrace purges absent rows whose removed_at is older than
// cleanup_after.
func (c *Cleaner) PurgeAbsentPastGrace() []PurgeResult {
	if c == nil || c.Store == nil || c.Config == nil {
		return nil
	}
	cutoff := GraceCutoffISO(c.Config.CleanupAfterSeconds, c.now())
	due, err := c.Store.ListAbsentPastGrace(cutoff)
	if err != nil {
		slog.Error("list absent past grace failed", "err", err)
		return []PurgeResult{{
			OK:    false,
			Error: err.Error(),
		}}
	}
	results := make([]PurgeResult, 0, len(due))
	for _, rec := range due {
		if rec == nil {
			continue
		}
		slog.Info("grace purge",
			"archive_id", rec.ArchiveID,
			"removed_at", valueOrEmpty(rec.RemovedAt),
			"cutoff", cutoff,
		)
		results = append(results, c.PurgeArchive(rec.ArchiveID, true))
	}
	if results == nil {
		results = []PurgeResult{}
	}
	return results
}

// PruneQuarantine deletes aged / over-cap quarantine entries under overlay_dir.
func (c *Cleaner) PruneQuarantine() (removed int, freed int64) {
	if c == nil || c.Config == nil {
		return 0, 0
	}
	retain := time.Duration(c.Config.QuarantineRetainForSeconds * float64(time.Second))
	return PruneQuarantine(
		c.Config.OverlayDir,
		retain,
		int64(c.Config.QuarantineMaxBytes),
		c.now(),
	)
}

// PruneOrphanRatarmountTemps removes unused .tmp* files under TmpDir (default /tmp).
func (c *Cleaner) PruneOrphanRatarmountTemps() (removed int, freed int64) {
	tmp := "/tmp"
	if c != nil && c.TmpDir != "" {
		tmp = c.TmpDir
	}
	var inUse PathInUseFunc
	if c != nil {
		inUse = c.PathInUse
	}
	return PruneOrphanRatarmountTemps(tmp, inUse)
}

// PruneStaleMountDirs removes orphan mount-point directories under mount_root.
func (c *Cleaner) PruneStaleMountDirs() []string {
	if c == nil || c.Config == nil {
		return nil
	}
	var live []string
	if c.LivePaths != nil {
		live = c.LivePaths()
	}
	return CleanupStaleMountDirs(c.Config, c.Store, c.IsMount, live)
}

// PruneNonsolidCache removes leftover outer-cache partials/stale locks and
// age-prunes orphaned {key}.7z under convert_7z_cache_dir (or default).
// Age threshold reuses cleanup_after; see PruneNonsolidCache package docs.
func (c *Cleaner) PruneNonsolidCache() NonsolidCachePruneResult {
	if c == nil || c.Config == nil {
		return NonsolidCachePruneResult{}
	}
	cacheDir := convert.DefaultNonsolidCacheDir(c.Config)
	var live []string
	if c.LivePaths != nil {
		live = c.LivePaths()
	}
	age := time.Duration(c.Config.CleanupAfterSeconds * float64(time.Second))
	return PruneNonsolidCache(cacheDir, age, c.now(), live)
}

// CheckDisk returns (lowDisk, freeBytesOrNil). lowDisk is true when free space
// is below min_free_bytes.
func (c *Cleaner) CheckDisk() (lowDisk bool, free *int64) {
	if c == nil || c.Config == nil {
		return false, nil
	}
	n, ok := FreeBytes(c.Config.OverlayDir)
	if !ok {
		n, ok = FreeBytes(c.Config.IndexDir)
	}
	if !ok {
		return false, nil
	}
	free = &n
	if n < int64(c.Config.MinFreeBytes) {
		slog.Error("low disk — pause new indexing",
			"free_bytes", n,
			"min_free_bytes", c.Config.MinFreeBytes,
		)
		return true, free
	}
	return false, free
}

// Run performs one cleaner pass: grace purges, quarantine prune, disk check,
// stale mount dirs, outer nonsolid cache hygiene. Ratarmount temp prune is
// available via PruneOrphanRatarmountTemps but is not part of the default pass
// (operator / serve may call it separately; Python run() also omits it from
// Cleaner.run).
func (c *Cleaner) Run() CleanerRunResult {
	result := CleanerRunResult{
		Purged:           []PurgeResult{},
		MountDirsRemoved: []string{},
		Errors:           []string{},
	}
	if c == nil {
		result.Errors = append(result.Errors, "cleaner is nil")
		return result
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("grace purge: %v", r))
			}
		}()
		result.Purged = c.PurgeAbsentPastGrace()
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("quarantine prune: %v", r))
			}
		}()
		n, freed := c.PruneQuarantine()
		result.QuarantinePruned = n
		result.QuarantineBytesFreed = freed
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("disk check: %v", r))
			}
		}()
		low, free := c.CheckDisk()
		result.LowDisk = low
		result.FreeBytes = free
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("mount dir cleanup: %v", r))
			}
		}()
		result.MountDirsRemoved = c.PruneStaleMountDirs()
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("nonsolid cache: %v", r))
			}
		}()
		ns := c.PruneNonsolidCache()
		result.NonsolidPartialsRemoved = ns.PartialsRemoved
		result.NonsolidLocksRemoved = ns.LocksRemoved
		result.NonsolidArchivesRemoved = ns.ArchivesRemoved
		result.NonsolidCacheBytesFreed = ns.BytesFreed
	}()

	return result
}

func valueOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
