package reconcile

import (
	"github.com/hilather/mount-wrapper/internal/mounter"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Apply executes one action against the store (and optional FS/callbacks).
// ActionOK is a no-op. Returns an updated action with ApplyError set on failure.
func Apply(store *state.Store, rec *state.ArchiveRecord, action Action, settings Settings, probes Probes, cb Callbacks) Action {
	if action.Kind == ActionOK || action.Kind == "" {
		return action
	}
	if store == nil || rec == nil {
		action.ApplyError = reconcileErrorf("apply: nil store or record")
		return action
	}
	settings = settings.Normalize()
	probes = probes.withDefaults()

	switch action.Kind {
	case ActionCleanupIndex:
		return applyCleanupIndex(store, rec, action)

	case ActionMarkAbsent:
		return applyMarkAbsent(store, rec, action, cb)

	case ActionFailIndex, ActionFailMount:
		return applyFail(store, rec, action, settings, probes, cb)

	case ActionRequeue:
		return applyRequeue(store, rec, action)

	case ActionRequestRemount:
		return applyRequestRemount(store, rec, action)

	default:
		action.ApplyError = reconcileErrorf("unknown action kind %q", action.Kind)
		return action
	}
}

func applyCleanupIndex(store *state.Store, rec *state.ArchiveRecord, action Action) Action {
	indexPath := strPtr(rec.IndexPath)
	deleted := mounter.ApplyPartialIndexCleanup(rec.Status, rec.FirstMountedAt, indexPath)
	if deleted {
		_, err := store.Transition(rec.ArchiveID, rec.Status, rec.Status, map[string]any{
			"index_path": nil,
		}, "")
		if err != nil {
			action.ApplyError = err
		}
	}
	return action
}

func applyMarkAbsent(store *state.Store, rec *state.ArchiveRecord, action Action, cb Callbacks) Action {
	if cb.DropLive != nil {
		cb.DropLive(rec.ArchiveID)
	}
	// Best-effort unmount before marking absent when a mount path is set.
	if rec.MountPath != nil && *rec.MountPath != "" && cb.UnmountIfMounted != nil {
		cb.UnmountIfMounted(*rec.MountPath)
	}
	reason := action.Reason
	_, err := store.MarkAbsent(rec.ArchiveID, "", &reason)
	if err != nil {
		action.ApplyError = err
	}
	action.TargetStatus = state.StatusAbsent
	return action
}

func applyFail(store *state.Store, rec *state.ArchiveRecord, action Action, settings Settings, probes Probes, cb Callbacks) Action {
	failStatus := state.StatusMountFailed
	if action.Kind == ActionFailIndex {
		failStatus = state.StatusIndexFailed
	}
	action.TargetStatus = failStatus

	attempts, retryable := mounter.NextMountAttempt(rec.MountAttempts, settings.MaxMountAttempts)

	if cb.DropLive != nil {
		cb.DropLive(rec.ArchiveID)
	}
	if rec.MountPath != nil && *rec.MountPath != "" && probes.IsMount(*rec.MountPath) {
		if cb.UnmountIfMounted != nil {
			cb.UnmountIfMounted(*rec.MountPath)
		}
	}

	// Partial index cleanup on first-index failure (or any fail_index).
	fields := map[string]any{
		"mount_pid":       nil,
		"mount_attempts":  attempts,
		"mount_retryable": retryable,
		"last_error":      action.Reason,
		// Intentionally do NOT touch hooks_status — remount must not re-run
		// terminal success (hooks.ShouldRunHooks).
	}

	deletePartial := action.Kind == ActionFailIndex ||
		rec.FirstMountedAt == nil || *rec.FirstMountedAt == ""
	if deletePartial {
		if rec.IndexPath != nil {
			mounter.DeleteIndexFile(*rec.IndexPath)
		}
		fields["index_path"] = nil
	}

	_, err := store.Transition(rec.ArchiveID, failStatus, rec.Status, fields, "")
	if err != nil {
		action.ApplyError = err
	}
	return action
}

func applyRequeue(store *state.Store, rec *state.ArchiveRecord, action Action) Action {
	target := action.TargetStatus
	if target == "" {
		target = requeueTarget(rec)
		action.TargetStatus = target
	}
	fields := map[string]any{
		"mount_pid": nil,
	}
	// Boot requeue of unmounting/converting sets mount_retryable true (Python).
	if rec.Status == state.StatusUnmounting || rec.Status == state.StatusConverting {
		fields["mount_retryable"] = true
	}
	// Same-status requeue: field-only clear of stale PID.
	if target == rec.Status {
		_, err := store.Transition(rec.ArchiveID, rec.Status, rec.Status, fields, "")
		if err != nil {
			action.ApplyError = err
		}
		return action
	}
	_, err := store.Transition(rec.ArchiveID, target, rec.Status, fields, "")
	if err != nil {
		action.ApplyError = err
	}
	return action
}

func applyRequestRemount(store *state.Store, rec *state.ArchiveRecord, action Action) Action {
	// mounted/hooks_running → mount_failed; clear PID; mark retryable.
	// hooks_status unchanged.
	fields := map[string]any{
		"mount_pid":       nil,
		"mount_retryable": true,
	}
	_, err := store.Transition(rec.ArchiveID, state.StatusMountFailed, rec.Status, fields, "")
	if err != nil {
		action.ApplyError = err
	}
	action.TargetStatus = state.StatusMountFailed
	return action
}
