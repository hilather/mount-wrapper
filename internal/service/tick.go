package service

import (
	"log/slog"
	"os"
	"sort"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/hooks"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Tick runs one service cycle (scan / reconcile / clean / progress / work / hooks).
// After scan/reconcile/cleanup or in-flight work/hooks activity, NotifyChange
// wakes SSE clients early; the SSE ticker remains the fallback.
func (s *Service) Tick() {
	if s == nil {
		return
	}

	dirty := false

	s.mu.Lock()
	reload := s.reloadRequested
	if reload {
		s.reloadRequested = false
	}
	s.mu.Unlock()
	if reload {
		s.doReload()
		dirty = true
	}

	// Control plane (non-blocking accept of pending connections).
	if s.control != nil {
		s.control.ServeReady()
	}

	if s.inotify != nil && s.inotify.Poll(0) {
		slog.Info("inotify activity; scheduling rescan")
		s.RequestRescan(false)
	}

	now := s.now()

	s.mu.Lock()
	rescan := s.rescanRequested || (now-s.lastScanAt >= s.Config.PollIntervalSeconds)
	assume := s.assumeStable
	if s.rescanRequested {
		s.rescanRequested = false
		s.assumeStable = false
	}
	s.mu.Unlock()

	if rescan {
		summary := s.doScan(assume)
		s.mu.Lock()
		s.lastScanAt = now
		s.lastScanResult = summary
		s.mu.Unlock()
		dirty = true
	}

	s.mu.Lock()
	needReconcile := now-s.lastReconcileAt >= s.Config.ReconcileIntervalSeconds
	s.mu.Unlock()
	if needReconcile {
		s.doReconcile()
		s.mu.Lock()
		s.lastReconcileAt = now
		s.mu.Unlock()
		dirty = true
	}

	cleanupEvery := s.Config.PollIntervalSeconds
	if cleanupEvery < 60 {
		cleanupEvery = 60
	}
	s.mu.Lock()
	needClean := now-s.lastCleanupAt >= cleanupEvery
	s.mu.Unlock()
	if needClean {
		s.doCleanup()
		s.mu.Lock()
		s.lastCleanupAt = now
		s.mu.Unlock()
		dirty = true
	}

	// Progress / convert / work can update status every tick while live.
	hadLiveWork := s.engineWorkActive()
	s.Engine.ProgressLive()
	s.Engine.PollRelocate()
	s.Engine.PollConvert()
	s.startPendingWork()
	hooksRan := s.runPendingHooks()
	if hadLiveWork || s.engineWorkActive() || hooksRan {
		dirty = true
	}

	if dirty {
		s.NotifyChange()
	}
}

// engineWorkActive reports live mounts or async convert/relocate jobs.
func (s *Service) engineWorkActive() bool {
	if s == nil || s.Engine == nil {
		return false
	}
	if s.Engine.Live != nil && s.Engine.Live.Len() > 0 {
		return true
	}
	if s.Engine.ConvertJobCount() > 0 || s.Engine.RelocateJobCount() > 0 {
		return true
	}
	return false
}

func (s *Service) doReload() {
	path := ""
	if s.Config != nil {
		path = s.Config.ConfigPath
	}
	if path == "" {
		slog.Warn("reload ignored: no config_path")
		// Still rematerialize scanner sources from current in-memory config.
		if s.Scanner != nil {
			if err := s.Scanner.ReloadSources(); err != nil {
				slog.Warn("reload sources failed", "err", err)
			}
		}
		return
	}
	newCfg, err := config.Load(path)
	if err != nil {
		slog.Error("reload failed", "err", err)
		return
	}
	s.Config = newCfg
	if s.Scanner != nil {
		s.Scanner.Config = newCfg
		if err := s.Scanner.ReloadSources(); err != nil {
			slog.Warn("reload sources failed", "err", err)
		}
	}
	if s.Engine != nil {
		s.Engine.Config = newCfg
		// Re-bind best-effort flatten probe when still default/nil (tests that
		// inject a custom NeedsFlatten keep it across reload).
		if s.Engine.NeedsFlatten == nil {
			s.Engine.NeedsFlatten = convert.DefaultFlattenNeeded(newCfg, s.Engine.ConvertOpts, nil)
		}
	}
	if s.Hooks != nil {
		s.Hooks.Config = newCfg
	}
	if s.Cleaner != nil {
		s.Cleaner.Config = newCfg
	}
	if s.Reconciler != nil {
		// Push hot-reloadable reconcile settings.
		s.Reconciler.Settings.MountReadyTimeoutSeconds = newCfg.MountReadyTimeoutSeconds
		s.Reconciler.Settings.MaxMountAttempts = newCfg.MaxMountAttempts
	}
	slog.Info("config reloaded", "path", path)
}

func (s *Service) doScan(assumeStable bool) map[string]any {
	slog.Info("scan starting", "event", "scan_start", "assume_stable", assumeStable)
	if n, err := s.Store.ResetAllPresentAttempts(""); err == nil && n > 0 {
		slog.Info("rescan reset mount_attempts", "count", n)
	}
	result, err := s.Scanner.Scan(assumeStable)
	if err != nil {
		slog.Error("scan failed", "err", err)
		return map[string]any{"error": err.Error(), "assume_stable": assumeStable}
	}
	summary := map[string]any{
		"duration_ms":     result.DurationMs,
		"seen":            len(result.Observations),
		"inserted":        len(result.InsertedIDs),
		"reappeared":      len(result.ReappearedIDs),
		"content_changed": len(result.ContentChangedIDs),
		"absent":          len(result.AbsentIDs),
		"stable":          len(result.StableArchiveIDs),
		"errors":          append([]string(nil), result.Errors...),
		"assume_stable":   assumeStable,
	}
	s.mu.Lock()
	s.lastScanAtISO = state.UTCNowISO()
	s.mu.Unlock()
	return summary
}

func (s *Service) doReconcile() map[string]any {
	result, err := s.Reconciler.Reconcile()
	if err != nil {
		slog.Error("reconcile failed", "err", err)
		return map[string]any{"error": err.Error()}
	}
	actions := make([]map[string]any, 0)
	for _, a := range result.Actions {
		if a.Kind == "ok" || a.Kind == "" {
			continue
		}
		actions = append(actions, map[string]any{
			"archive_id": a.ArchiveID,
			"action":     string(a.Kind),
			"reason":     a.Reason,
		})
	}
	if len(actions) > 0 {
		slog.Info("reconcile actions", "count", len(actions))
	}
	return map[string]any{"actions": actions}
}

func (s *Service) doCleanup() map[string]any {
	result := s.Cleaner.Run()
	s.mu.Lock()
	s.lowDisk = result.LowDisk
	s.mu.Unlock()
	summary := map[string]any{
		"purged":                 result.PurgedIDs(),
		"purged_count":           len(result.PurgedIDs()),
		"quarantine_pruned":      result.QuarantinePruned,
		"quarantine_bytes_freed": result.QuarantineBytesFreed,
		"mount_dirs_removed":     result.MountDirsRemoved,
		"low_disk":               result.LowDisk,
		"free_bytes":             result.FreeBytes,
		"errors":                 append([]string(nil), result.Errors...),
	}
	s.mu.Lock()
	s.lastCleanupResult = summary
	s.mu.Unlock()
	if len(result.PurgedIDs()) > 0 || result.QuarantinePruned > 0 || len(result.MountDirsRemoved) > 0 {
		slog.Info("cleanup",
			"purged", len(result.PurgedIDs()),
			"quarantine_pruned", result.QuarantinePruned,
			"mount_dirs_removed", len(result.MountDirsRemoved),
			"low_disk", result.LowDisk,
		)
	}
	return summary
}

// SortArchivesForIndex orders candidates; default smallest size_bytes first.
func SortArchivesForIndex(records []*state.ArchiveRecord, smallestFirst bool) []*state.ArchiveRecord {
	out := append([]*state.ArchiveRecord(nil), records...)
	if !smallestFirst {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i].SizeBytes, out[j].SizeBytes
		if si != sj {
			return si < sj
		}
		bi, bj := out[i].ArchiveBasename, out[j].ArchiveBasename
		if bi != bj {
			return bi < bj
		}
		return out[i].ArchiveID < out[j].ArchiveID
	})
	return out
}

func (s *Service) startPendingWork() {
	lowDisk := s.LowDisk()
	indexingSlots := s.Config.MaxConcurrentIndex
	if lowDisk {
		slog.Debug("skip new indexing: low disk")
		indexingSlots = 0
	}

	indexing := s.Engine.ActiveIndexCount()
	mounting := s.Engine.ActiveMountCount()
	converting := s.countStatus(state.StatusConverting)

	s.retryStuckIndexWork()

	// First-time index from stable discovered.
	if indexing < indexingSlots {
		recs, err := s.Store.ListArchives(state.StatusDiscovered)
		if err != nil {
			return
		}
		discovered := SortArchivesForIndex(recs, s.Config.IndexSmallestFirst)
		for _, rec := range discovered {
			if indexing >= s.Config.MaxConcurrentIndex {
				break
			}
			if s.Engine.HasRelocateJob(rec.ArchiveID) || s.Engine.HasConvertJob(rec.ArchiveID) {
				continue
			}
			if !s.Scanner.IsStable(rec.ArchivePath) {
				continue
			}
			if !fileExists(rec.ArchivePath) {
				continue
			}
			if !rec.MountRetryable {
				continue
			}
			if convert.ShouldPreconvert(s.Config, rec.ArchivePath, s.Engine.ConvertOpts, s.Engine.NeedsFlatten) {
				if converting >= s.Config.MaxConcurrentConvert {
					continue
				}
			}
			first := true
			managed, err := s.Engine.BeginMount(rec, &first)
			if err != nil {
				slog.Error("begin_mount failed", "event", "begin_mount_failed", "archive_id", rec.ArchiveID, "err", err)
				continue
			}
			if managed == nil {
				if s.Engine.HasConvertJob(rec.ArchiveID) {
					converting++
					slog.Info("convert queued", "event", "convert_queued", "archive_id", rec.ArchiveID, "path", rec.ArchivePath)
				}
				continue
			}
			indexing++
			slog.Info("index start", "event", "index_start", "archive_id", rec.ArchiveID, "path", rec.ArchivePath)
		}
	}

	// Retry mount_failed when retryable.
	failed, err := s.Store.ListArchives(state.StatusMountFailed)
	if err != nil {
		return
	}
	for _, rec := range failed {
		if !rec.MountRetryable {
			continue
		}
		if !fileExists(rec.ArchivePath) {
			continue
		}
		if s.Engine.HasConvertJob(rec.ArchiveID) {
			continue
		}
		if convert.ShouldPreconvert(s.Config, rec.ArchivePath, s.Engine.ConvertOpts, s.Engine.NeedsFlatten) {
			if converting >= s.Config.MaxConcurrentConvert {
				continue
			}
		}
		needsIndex := mounter.NeedsFreshIndex(strPtr(rec.IndexPath))
		if needsIndex {
			if indexing >= s.Config.MaxConcurrentIndex {
				break
			}
		} else if mounter.LimitReached(mounting, s.Config.MaxConcurrentMount) {
			break
		}
		first := false
		managed, err := s.Engine.BeginMount(rec, &first)
		if err != nil {
			slog.Error("remount failed", "event", "remount_failed", "archive_id", rec.ArchiveID, "err", err)
			continue
		}
		if managed == nil {
			if s.Engine.HasConvertJob(rec.ArchiveID) {
				converting++
			}
			continue
		}
		if needsIndex {
			indexing++
		} else {
			mounting++
		}
	}
}

func (s *Service) retryStuckIndexWork() {
	for _, st := range []string{state.StatusIndexing, state.StatusMounting} {
		recs, err := s.Store.ListArchives(st)
		if err != nil {
			continue
		}
		for _, rec := range recs {
			if s.Engine.HasRelocateJob(rec.ArchiveID) || s.Engine.HasConvertJob(rec.ArchiveID) {
				continue
			}
			if s.Engine.Live.Get(rec.ArchiveID) != nil {
				continue
			}
			if rec.MountPID != nil && mounter.IsProcessAlive(int(*rec.MountPID)) {
				continue
			}
			if !fileExists(rec.ArchivePath) {
				continue
			}
			if !rec.MountRetryable {
				continue
			}
			first := rec.FirstMountedAt == nil
			if _, err := s.Engine.BeginMount(rec, &first); err != nil {
				slog.Error("retry stuck index/mount failed", "event", "retry_stuck_failed", "archive_id", rec.ArchiveID, "err", err)
			}
		}
	}
}

// runPendingHooks runs first-mount hooks for eligible mounted archives.
// Returns true when at least one hook cycle ran (status may have changed).
func (s *Service) runPendingHooks() bool {
	recs, err := s.Store.ListArchives(state.StatusMounted)
	if err != nil {
		return false
	}
	ranAny := false
	for _, rec := range recs {
		if !hooks.ShouldRunHooksRecord(rec, s.Config.HookRerunOnFailure) {
			continue
		}
		result, err := s.Hooks.RunForArchive(rec.ArchiveID, false)
		if err != nil {
			slog.Error("hooks failed", "event", "hooks_failed", "archive_id", rec.ArchiveID, "err", err)
			continue
		}
		if result != nil && result.Ran {
			ranAny = true
			slog.Info("hooks cycle",
				"event", "hooks_cycle",
				"archive_id", rec.ArchiveID,
				"ran", result.Ran,
				"status", result.HooksStatus,
			)
		}
	}
	return ranAny
}

func (s *Service) countStatus(statuses ...string) int {
	n := 0
	for _, st := range statuses {
		recs, err := s.Store.ListArchives(st)
		if err != nil {
			continue
		}
		n += len(recs)
	}
	return n
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func strPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
