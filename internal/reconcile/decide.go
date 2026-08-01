package reconcile

import (
	"fmt"
	"strings"
	"time"

	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

// DecideOne returns the pure corrective action for one archive record.
// Returns nil when no action is needed (idle statuses without partial index).
//
// Does not mutate the store or filesystem.
func DecideOne(rec *state.ArchiveRecord, settings Settings, probes Probes) *Action {
	if rec == nil {
		return nil
	}
	settings = settings.Normalize()
	probes = probes.withDefaults()

	switch rec.Status {
	case state.StatusAbsent:
		return nil

	case state.StatusIndexing, state.StatusMounting, state.StatusConverting:
		return checkInProgress(rec, settings, probes)

	case state.StatusMounted, state.StatusHooksRunning:
		return checkHealthyMount(rec, probes)

	case state.StatusUnmounting:
		// Leave unmounting to mounter; if archive gone, mark absent.
		if !probes.PathExists(rec.ArchivePath) {
			return &Action{
				ArchiveID:      rec.ArchiveID,
				Kind:           ActionMarkAbsent,
				Reason:         "archive missing during unmount",
				PreviousStatus: rec.Status,
				TargetStatus:   state.StatusAbsent,
			}
		}
		return nil

	case state.StatusDiscovered, state.StatusIndexFailed, state.StatusMountFailed:
		return maybeCleanupIndex(rec, probes)

	default:
		return nil
	}
}

func maybeCleanupIndex(rec *state.ArchiveRecord, probes Probes) *Action {
	indexPath := strPtr(rec.IndexPath)
	if !mounter.ShouldDeletePartialIndex(rec.Status, rec.FirstMountedAt, indexPath) {
		return nil
	}
	if !probes.IndexIsFile(indexPath) {
		return nil
	}
	return &Action{
		ArchiveID:      rec.ArchiveID,
		Kind:           ActionCleanupIndex,
		Reason:         "partial index without successful mount",
		PreviousStatus: rec.Status,
	}
}

func checkInProgress(rec *state.ArchiveRecord, settings Settings, probes Probes) *Action {
	status := rec.Status

	// converting: fail when no convert job is supervised.
	if status == state.StatusConverting {
		active := probes.ConvertActive != nil && probes.ConvertActive(rec.ArchiveID)
		if active {
			return &Action{
				ArchiveID:      rec.ArchiveID,
				Kind:           ActionOK,
				Reason:         "convert job still running",
				PreviousStatus: status,
			}
		}
		return &Action{
			ArchiveID:      rec.ArchiveID,
			Kind:           ActionFailMount,
			Reason:         "7z flatten interrupted",
			PreviousStatus: status,
			TargetStatus:   state.StatusMountFailed,
		}
	}

	pidDead := false
	if rec.MountPID != nil {
		pid := int(*rec.MountPID)
		if !probes.PIDAlive(pid) {
			pidDead = true
		}
	}

	// Supervised live map (optional).
	if probes.Live != nil {
		if live := probes.Live(rec.ArchiveID); live != nil {
			if live.Exited {
				if live.Phase == mounter.PhaseIndexOnly &&
					mounter.IndexBuildVerified(live.IndexPath, live.ArchivePath, live.ExitCode, live.MountBackend) {
					return &Action{
						ArchiveID:      rec.ArchiveID,
						Kind:           ActionOK,
						Reason:         "index build finished (mounter should start mount phase)",
						PreviousStatus: status,
					}
				}
				pidDead = true
			} else if live.Phase == mounter.PhaseMount &&
				live.MountPath != "" &&
				probes.IsMount(live.MountPath) &&
				mounter.MountIndexRequirementMet(live.IndexPath, live.ArchivePath, live.MountBackend) {
				return &Action{
					ArchiveID:      rec.ArchiveID,
					Kind:           ActionOK,
					Reason:         "process live and mount ready (mounter should promote)",
					PreviousStatus: status,
				}
			}
		}
	}

	timedOut := false
	if started := parseISOToEpoch(strPtr(rec.IndexStartedAt)); started != nil {
		elapsed := probes.Clock() - *started
		if elapsed > settings.MountReadyTimeoutSeconds {
			timedOut = true
		}
	}

	if pidDead {
		return failInProgress(rec, "ratarmount process dead before mount ready")
	}
	if timedOut {
		return failInProgress(rec, fmt.Sprintf(
			"mount_ready timeout (%gs)", settings.MountReadyTimeoutSeconds,
		))
	}

	// Explicitly OK: long index without ismount is expected.
	return &Action{
		ArchiveID:      rec.ArchiveID,
		Kind:           ActionOK,
		Reason:         "in-progress index/mount still running",
		PreviousStatus: status,
	}
}

func failInProgress(rec *state.ArchiveRecord, reason string) *Action {
	if rec.Status == state.StatusIndexing {
		return &Action{
			ArchiveID:      rec.ArchiveID,
			Kind:           ActionFailIndex,
			Reason:         reason,
			PreviousStatus: rec.Status,
			TargetStatus:   state.StatusIndexFailed,
		}
	}
	return &Action{
		ArchiveID:      rec.ArchiveID,
		Kind:           ActionFailMount,
		Reason:         reason,
		PreviousStatus: rec.Status,
		TargetStatus:   state.StatusMountFailed,
	}
}

func checkHealthyMount(rec *state.ArchiveRecord, probes Probes) *Action {
	mountOK := rec.MountPath != nil && *rec.MountPath != "" && probes.IsMount(*rec.MountPath)
	pidOK := false
	if rec.MountPID != nil {
		pidOK = probes.PIDAlive(int(*rec.MountPID))
	}

	if mountOK && pidOK {
		return &Action{
			ArchiveID:      rec.ArchiveID,
			Kind:           ActionOK,
			Reason:         "mount healthy",
			PreviousStatus: rec.Status,
		}
	}

	reason := fmt.Sprintf("mount unhealthy (ismount=%v, pid_alive=%v)", mountOK, pidOK)
	if !probes.PathExists(rec.ArchivePath) {
		return &Action{
			ArchiveID:      rec.ArchiveID,
			Kind:           ActionMarkAbsent,
			Reason:         reason + " and archive missing",
			PreviousStatus: rec.Status,
			TargetStatus:   state.StatusAbsent,
		}
	}

	// Archive still present — fail remount path; do not touch hooks_status
	// (terminal success must not re-run; hooks.ShouldRunHooks enforces this).
	return &Action{
		ArchiveID:      rec.ArchiveID,
		Kind:           ActionFailMount,
		Reason:         reason,
		PreviousStatus: rec.Status,
		TargetStatus:   state.StatusMountFailed,
	}
}

// PlanBoot decides boot-time remount / requeue actions for one archive.
// Does not include partial-index cleanup (use DecideOne / CleanupPartialIndexes).
func PlanBoot(rec *state.ArchiveRecord, probes Probes) *Action {
	if rec == nil || rec.Status == state.StatusAbsent {
		return nil
	}
	probes = probes.withDefaults()

	switch rec.Status {
	case state.StatusIndexing, state.StatusMounting:
		target := requeueTarget(rec)
		return &Action{
			ArchiveID:      rec.ArchiveID,
			Kind:           ActionRequeue,
			Reason:         "boot: previous process died mid-index/mount",
			PreviousStatus: rec.Status,
			TargetStatus:   target,
		}

	case state.StatusUnmounting:
		if !probes.PathExists(rec.ArchivePath) {
			return &Action{
				ArchiveID:      rec.ArchiveID,
				Kind:           ActionMarkAbsent,
				Reason:         "boot: archive missing during unmount",
				PreviousStatus: rec.Status,
				TargetStatus:   state.StatusAbsent,
			}
		}
		return &Action{
			ArchiveID:      rec.ArchiveID,
			Kind:           ActionRequeue,
			Reason:         "boot: interrupted unmount; re-queue",
			PreviousStatus: rec.Status,
			TargetStatus:   requeueTarget(rec),
		}

	case state.StatusConverting:
		if !probes.PathExists(rec.ArchivePath) {
			return &Action{
				ArchiveID:      rec.ArchiveID,
				Kind:           ActionMarkAbsent,
				Reason:         "boot: archive missing during convert",
				PreviousStatus: rec.Status,
				TargetStatus:   state.StatusAbsent,
			}
		}
		return &Action{
			ArchiveID:      rec.ArchiveID,
			Kind:           ActionRequeue,
			Reason:         "boot: interrupted convert; re-queue",
			PreviousStatus: rec.Status,
			TargetStatus:   requeueTarget(rec),
		}

	case state.StatusMounted, state.StatusHooksRunning:
		if !probes.PathExists(rec.ArchivePath) {
			return &Action{
				ArchiveID:      rec.ArchiveID,
				Kind:           ActionMarkAbsent,
				Reason:         "boot: archive missing; was mounted",
				PreviousStatus: rec.Status,
				TargetStatus:   state.StatusAbsent,
			}
		}
		// Force remount via mount_failed → mounting work queue.
		// hooks_status is intentionally left unchanged (no terminal-success re-run).
		return &Action{
			ArchiveID:      rec.ArchiveID,
			Kind:           ActionRequestRemount,
			Reason:         "boot: clear stale PID and request remount",
			PreviousStatus: rec.Status,
			TargetStatus:   state.StatusMountFailed,
		}

	default:
		// discovered / index_failed / mount_failed: no boot remount; PID clear only if set.
		if rec.MountPID != nil {
			return &Action{
				ArchiveID:      rec.ArchiveID,
				Kind:           ActionRequeue,
				Reason:         "boot: clear stale PID",
				PreviousStatus: rec.Status,
				TargetStatus:   rec.Status, // field-only patch (clear pid)
			}
		}
		return nil
	}
}

func requeueTarget(rec *state.ArchiveRecord) string {
	// mounting → discovered is not in ALLOWED_TRANSITIONS (parity with state
	// machine). First-time work uses indexing; remount uses mounting and should
	// land on mount_failed for the remount queue.
	if rec.Status == state.StatusMounting {
		return state.StatusMountFailed
	}
	if rec.FirstMountedAt == nil || *rec.FirstMountedAt == "" {
		return state.StatusDiscovered
	}
	return state.StatusMountFailed
}

func strPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// parseISOToEpoch parses ISO-8601 timestamps (with optional trailing Z) to Unix seconds.
func parseISOToEpoch(value string) *float64 {
	text := strings.TrimSpace(value)
	if text == "" {
		return nil
	}
	// Accept trailing Z.
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.000000Z",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, text); err == nil {
			sec := float64(t.UnixNano()) / 1e9
			return &sec
		}
	}
	// Python: if ends with Z, replace with +00:00 for fromisoformat.
	if strings.HasSuffix(text, "Z") {
		alt := strings.TrimSuffix(text, "Z") + "+00:00"
		if t, err := time.Parse(time.RFC3339Nano, alt); err == nil {
			sec := float64(t.UnixNano()) / 1e9
			return &sec
		}
		if t, err := time.Parse(time.RFC3339, alt); err == nil {
			sec := float64(t.UnixNano()) / 1e9
			return &sec
		}
	}
	return nil
}
