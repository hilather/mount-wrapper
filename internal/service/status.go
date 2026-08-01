package service

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/hilather/mount-wrapper/internal/cleaner"
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
	"github.com/hilather/mount-wrapper/internal/status"
)

// StatusPayload is the rich status document (alias of status.Payload).
type StatusPayload = status.Payload

// StatusPayload builds the control/status response body (include_sizes=false).
func (s *Service) StatusPayload() *StatusPayload {
	return s.StatusPayloadOpts(false)
}

// StatusPayloadOpts builds the typed status document, optionally merging metrics.
func (s *Service) StatusPayloadOpts(includeSizes bool) *StatusPayload {
	return status.Build(s.statusOptions(includeSizes))
}

// StatusMap builds the control/status JSON document as a map (socket/SPA friendly).
// When includeSizes is true, attaches per-archive metrics and metrics_summary.
func (s *Service) StatusMap(includeSizes bool) map[string]any {
	p := s.StatusPayloadOpts(includeSizes)
	if p == nil {
		return map[string]any{
			"version":  "dev",
			"pid":      0,
			"counts":   map[string]int{},
			"archives": []any{},
		}
	}
	// Encode via JSON so nested structs (metrics, archives) become map-friendly.
	b, err := json.Marshal(p)
	if err != nil {
		return map[string]any{"version": p.Version, "pid": p.PID, "error": err.Error()}
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]any{"version": p.Version, "pid": p.PID, "error": err.Error()}
	}
	return out
}

func (s *Service) statusOptions(includeSizes bool) status.Options {
	if s == nil {
		return status.Options{}
	}

	s.mu.Lock()
	lastScanAt := s.lastScanAtISO
	lowDisk := s.lowDisk
	lastScan := s.lastScanResult
	lastCleanup := s.lastCleanupResult
	s.mu.Unlock()

	var archives []*status.ArchiveInput
	if s.Store != nil {
		if recs, err := s.Store.ListArchives(nil); err == nil {
			archives = status.FromStateRecords(recs)
		}
	}

	live := map[string]status.LiveMount{}
	var isMount func(string) bool
	if s.Engine != nil {
		if s.Engine.IsMount != nil {
			isMount = s.Engine.IsMount
		}
		if s.Engine.Live != nil {
			byID := make(map[string]*status.ArchiveInput, len(archives))
			for _, a := range archives {
				if a != nil {
					byID[a.ArchiveID] = a
				}
			}
			for id, m := range s.Engine.Live.Snapshot() {
				if m == nil {
					continue
				}
				skips := m.NestedSkips()
				skipSum := mounter.FormatNestedSkipSummary(skips, mounter.DefaultNestedSkipSamples)
				lm := status.LiveMount{
					PID:          m.PID,
					Phase:        string(m.Phase),
					IsFirstIndex: m.IsFirstIndex,
				}
				if len(skips) > 0 {
					lm.NestedSkipsCount = len(skips)
					lm.NestedSkipsSummary = skipSum
				}
				live[id] = lm
				// Prefer live PID/path when present; attach nested skip fields.
				if a, ok := byID[id]; ok {
					pid := int64(m.PID)
					a.MountPID = &pid
					if m.Request.MountPath != "" {
						mp := m.Request.MountPath
						a.MountPath = &mp
					}
					if len(skips) > 0 {
						n := len(skips)
						a.NestedSkipsCount = &n
						a.NestedSkipsSummary = skipSum
					}
				}
			}
		}
	}
	// Persist-derived fallback: last_error may hold pure skip summary (mounted)
	// or failure reason + "; skipped N …" when live entry is gone.
	for _, a := range archives {
		if a == nil || a.NestedSkipsCount != nil {
			continue
		}
		if a.LastError == nil {
			continue
		}
		if sum, n := mounter.ExtractNestedSkipSummary(*a.LastError); n > 0 {
			a.NestedSkipsCount = &n
			a.NestedSkipsSummary = sum
		}
	}

	cfgPath := ""
	overlayDir := ""
	indexDir := ""
	minFree := 0
	maxAttempts := 10
	if s.Config != nil {
		cfgPath = s.Config.ConfigPath
		overlayDir = s.Config.OverlayDir
		indexDir = s.Config.IndexDir
		minFree = s.Config.MinFreeBytes
		maxAttempts = s.Config.MaxMountAttempts
	}

	return status.Options{
		Version:          s.Version,
		PID:              os.Getpid(),
		ConfigPath:       cfgPath,
		OverlayDir:       overlayDir,
		IndexDir:         indexDir,
		MinFreeBytes:     minFree,
		MaxMountAttempts: maxAttempts,
		Archives:         archives,
		Live:             live,
		LastScan:         lastScan,
		LastCleanup:      lastCleanup,
		LastScanAt:       lastScanAt,
		LowDisk:          lowDisk,
		Now:              s.now(),
		Clock:            s.Clock,
		PIDAlive:         mounter.IsProcessAlive,
		FreeBytes:        cleaner.FreeBytes,
		IsMount:          isMount,
		IncludeSizes:     includeSizes,
		Metrics:          s.Metrics,
	}
}

// archiveDict is a JSON-friendly archive snapshot for control ops.
func archiveDict(rec *state.ArchiveRecord) map[string]any {
	if rec == nil {
		return nil
	}
	m := map[string]any{
		"archive_id":       rec.ArchiveID,
		"archive_path":     rec.ArchivePath,
		"archive_basename": rec.ArchiveBasename,
		"status":           rec.Status,
		"hooks_status":     rec.HooksStatus,
		"size_bytes":       rec.SizeBytes,
		"mount_retryable":  rec.MountRetryable,
		"mount_attempts":   rec.MountAttempts,
	}
	if rec.MountPath != nil {
		m["mount_path"] = *rec.MountPath
	}
	if rec.MountPID != nil {
		m["mount_pid"] = *rec.MountPID
	}
	if rec.LastError != nil {
		m["last_error"] = *rec.LastError
	}
	if rec.IndexPath != nil {
		m["index_path"] = *rec.IndexPath
	}
	return m
}

// resolveTarget finds an archive by id, mount path basename, or archive path.
func (s *Service) resolveTarget(target string) *state.ArchiveRecord {
	if s == nil || s.Store == nil || target == "" {
		return nil
	}
	if rec, _ := s.Store.GetArchive(target); rec != nil {
		return rec
	}
	if rec, _ := s.Store.GetArchiveByPath(target); rec != nil {
		return rec
	}
	// Match mount_path or basename.
	recs, err := s.Store.ListArchives(nil)
	if err != nil {
		return nil
	}
	base := filepath.Base(target)
	for _, rec := range recs {
		if rec.MountPath != nil && (*rec.MountPath == target || filepath.Base(*rec.MountPath) == base) {
			return rec
		}
		if rec.ArchiveBasename == base || rec.ArchiveBasename == target {
			return rec
		}
	}
	return nil
}
