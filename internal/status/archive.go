package status

import (
	"sort"
)

// ArchiveToDict serializes one archive with progress fields when in-progress.
func ArchiveToDict(rec *ArchiveInput, now float64, live map[string]LiveMount, pidAlive func(int) bool, isMount func(string) bool) ArchiveDict {
	if rec == nil {
		return ArchiveDict{}
	}
	d := ArchiveDict{
		ArchiveID:              rec.ArchiveID,
		ArchivePath:            rec.ArchivePath,
		ArchiveBasename:        rec.ArchiveBasename,
		SourceDir:              rec.SourceDir,
		Status:                 rec.Status,
		HooksStatus:            rec.HooksStatus,
		MountPath:              rec.MountPath,
		IndexPath:              rec.IndexPath,
		OverlayPath:            rec.OverlayPath,
		MountPID:               rec.MountPID,
		MountAttempts:          rec.MountAttempts,
		MountRetryable:         rec.MountRetryable,
		Fingerprint:            rec.Fingerprint,
		SizeBytes:              rec.SizeBytes,
		LastError:              rec.LastError,
		LastSeenAt:             rec.LastSeenAt,
		RemovedAt:              rec.RemovedAt,
		FirstMountedAt:         rec.FirstMountedAt,
		HooksCompletedAt:       rec.HooksCompletedAt,
		IndexStartedAt:         rec.IndexStartedAt,
		IndexDurationSeconds:   rec.IndexDurationSeconds,
		MountDurationSeconds:   rec.MountDurationSeconds,
		ConvertSourceSizeBytes: rec.ConvertSourceSizeBytes,
		ConvertDurationSeconds: rec.ConvertDurationSeconds,
		SourceFS:               SourceFSLabel(rec.ArchivePath),
		PIDAlive:               pidAliveCheck(pidAlive, rec.MountPID),
	}
	if isMount != nil {
		mounted := false
		if rec.MountPath != nil && *rec.MountPath != "" {
			mounted = isMount(*rec.MountPath)
		}
		d.IsMounted = &mounted
	}

	// Nested skip fields: prefer ArchiveInput (service may pre-fill from live or
	// last_error), then live map, then last_error-derived values set on input.
	if rec.NestedSkipsCount != nil && *rec.NestedSkipsCount > 0 {
		n := *rec.NestedSkipsCount
		d.NestedSkipsCount = &n
		d.NestedSkipsSummary = rec.NestedSkipsSummary
	}
	lm, hasLive := live[rec.ArchiveID]
	if hasLive && lm.NestedSkipsCount > 0 {
		n := lm.NestedSkipsCount
		d.NestedSkipsCount = &n
		if lm.NestedSkipsSummary != "" {
			d.NestedSkipsSummary = lm.NestedSkipsSummary
		}
	}

	if isInProgress(rec.Status) {
		started := ""
		if rec.IndexStartedAt != nil {
			started = *rec.IndexStartedAt
		}
		if elapsed := ElapsedSeconds(started, now, nil); elapsed != nil {
			r := Round1(*elapsed)
			d.ElapsedS = &r
		}
		d.ProgressLabel = progressLabel(rec.Status, lm, hasLive)
		if hasLive {
			pid := lm.PID
			d.LivePID = &pid
			first := lm.IsFirstIndex
			d.IsFirstIndex = &first
			d.MountPhase = lm.Phase
		}
	} else if rec.Status == "hooks_running" {
		d.ProgressLabel = "hooks"
	}
	return d
}

// progressLabel chooses a human phase string for in-progress work.
// Live phase wins when present ("building index" / "mounting FUSE").
func progressLabel(status string, lm LiveMount, hasLive bool) string {
	if hasLive {
		if lm.Phase == "index_only" {
			return "building index"
		}
		if lm.Phase == "mount" {
			return "mounting FUSE"
		}
	}
	switch status {
	case "converting":
		return "converting to non-solid"
	case "indexing":
		return "indexing"
	case "mounting":
		return "mounting"
	case "hooks_running":
		return "hooks"
	default:
		return ""
	}
}

// BuildIndexingArchives returns a compact list of in-progress jobs, longest first.
func BuildIndexingArchives(archives []*ArchiveInput, now float64, live map[string]LiveMount, pidAlive func(int) bool) []IndexingJob {
	out := make([]IndexingJob, 0)
	for _, rec := range archives {
		if rec == nil || !isInProgress(rec.Status) {
			continue
		}
		started := ""
		if rec.IndexStartedAt != nil {
			started = *rec.IndexStartedAt
		}
		var elapsedRounded *float64
		if elapsed := ElapsedSeconds(started, now, nil); elapsed != nil {
			r := Round1(*elapsed)
			elapsedRounded = &r
		}
		entry := IndexingJob{
			ArchiveID: rec.ArchiveID,
			Path:      rec.ArchivePath,
			Basename:  rec.ArchiveBasename,
			Status:    rec.Status,
			ElapsedS:  elapsedRounded,
			SourceFS:  SourceFSLabel(rec.ArchivePath),
			MountPID:  rec.MountPID,
			PIDAlive:  pidAliveCheck(pidAlive, rec.MountPID),
		}
		lm, hasLive := live[rec.ArchiveID]
		entry.ProgressLabel = progressLabel(rec.Status, lm, hasLive)
		if hasLive {
			pid := lm.PID
			entry.LivePID = &pid
			entry.MountPhase = lm.Phase
		}
		out = append(out, entry)
	}
	// Longest-running first; nil elapsed last.
	sort.SliceStable(out, func(i, j int) bool {
		ei, ej := out[i].ElapsedS, out[j].ElapsedS
		if ei == nil && ej == nil {
			return false
		}
		if ei == nil {
			return false
		}
		if ej == nil {
			return true
		}
		return *ei > *ej
	})
	return out
}

// BuildErrorsRecent returns failed / stuck archives for the status surface.
func BuildErrorsRecent(archives []*ArchiveInput, maxMountAttempts, limit int) []ErrorEntry {
	if limit <= 0 {
		limit = 20
	}
	errors := make([]ErrorEntry, 0)
	for _, rec := range archives {
		if rec == nil {
			continue
		}
		stuck := (!rec.MountRetryable) || (rec.MountAttempts >= maxMountAttempts)
		failed := isErrorStatus(rec.Status)
		if !failed && !stuck {
			continue
		}
		errors = append(errors, ErrorEntry{
			ArchiveID:      rec.ArchiveID,
			Basename:       rec.ArchiveBasename,
			Path:           rec.ArchivePath,
			Status:         rec.Status,
			MountAttempts:  rec.MountAttempts,
			MountRetryable: rec.MountRetryable,
			LastError:      rec.LastError,
			Stuck:          stuck,
		})
	}
	// Prefer failed statuses, then higher attempt counts.
	sort.SliceStable(errors, func(i, j int) bool {
		fi := 1
		if isErrorStatus(errors[i].Status) {
			fi = 0
		}
		fj := 1
		if isErrorStatus(errors[j].Status) {
			fj = 0
		}
		if fi != fj {
			return fi < fj
		}
		return errors[i].MountAttempts > errors[j].MountAttempts
	})
	if len(errors) > limit {
		errors = errors[:limit]
	}
	return errors
}
