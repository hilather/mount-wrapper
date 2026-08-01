package state

import "sort"

// Archive lifecycle statuses (single source of truth — matches design + CHECK).
const (
	StatusDiscovered   = "discovered"
	StatusConverting   = "converting"
	StatusIndexing     = "indexing"
	StatusIndexFailed  = "index_failed"
	StatusMounting     = "mounting"
	StatusMountFailed  = "mount_failed"
	StatusMounted      = "mounted"
	StatusHooksRunning = "hooks_running"
	StatusUnmounting   = "unmounting"
	StatusAbsent       = "absent"
)

// Aggregate hooks_status values on the archive row.
const (
	HooksNone    = "none"
	HooksPending = "pending"
	HooksRunning = "running"
	HooksSuccess = "success"
	HooksFailed  = "failed"
	HooksRetry   = "retry"
)

// Per-hook row statuses.
const (
	HookPending = "pending"
	HookRunning = "running"
	HookSuccess = "success"
	HookFailed  = "failed"
	HookRetry   = "retry"
	HookSkipped = "skipped"
)

// ARCHIVE_STATUSES is the full set of archive lifecycle statuses.
var ARCHIVE_STATUSES = map[string]struct{}{
	StatusDiscovered:   {},
	StatusConverting:   {},
	StatusIndexing:     {},
	StatusIndexFailed:  {},
	StatusMounting:     {},
	StatusMountFailed:  {},
	StatusMounted:      {},
	StatusHooksRunning: {},
	StatusUnmounting:   {},
	StatusAbsent:       {},
}

// HOOKS_STATUSES is the set of archive-level hooks_status values.
var HOOKS_STATUSES = map[string]struct{}{
	HooksNone:    {},
	HooksPending: {},
	HooksRunning: {},
	HooksSuccess: {},
	HooksFailed:  {},
	HooksRetry:   {},
}

// HOOK_ROW_STATUSES is the set of per-hook row status values.
var HOOK_ROW_STATUSES = map[string]struct{}{
	HookPending: {},
	HookRunning: {},
	HookSuccess: {},
	HookFailed:  {},
	HookRetry:   {},
	HookSkipped: {},
}

// ALLOWED_TRANSITIONS is the directed state machine (parity with Python state.py).
// Purge is DELETE, not a status transition.
var ALLOWED_TRANSITIONS = map[string]map[string]struct{}{
	StatusDiscovered: setOf(StatusConverting, StatusIndexing, StatusMounting, StatusUnmounting, StatusAbsent),
	StatusConverting: setOf(
		StatusDiscovered, StatusIndexing, StatusMounting, StatusMountFailed,
		StatusIndexFailed, StatusUnmounting, StatusAbsent,
	),
	StatusIndexing: setOf(
		StatusIndexFailed, StatusMounted, StatusHooksRunning, StatusUnmounting,
		StatusDiscovered, StatusMounting, StatusMountFailed,
	),
	StatusIndexFailed: setOf(StatusDiscovered, StatusUnmounting, StatusAbsent),
	StatusMounting:    setOf(StatusMounted, StatusMountFailed, StatusHooksRunning, StatusUnmounting, StatusIndexing),
	// mount_failed: reconcile when FUSE/PID dies but archive still present (remount path)
	StatusMountFailed:  setOf(StatusConverting, StatusMounting, StatusDiscovered, StatusUnmounting, StatusAbsent, StatusIndexing),
	StatusMounted:      setOf(StatusHooksRunning, StatusUnmounting, StatusMounting, StatusMountFailed),
	StatusHooksRunning: setOf(StatusMounted, StatusUnmounting, StatusMountFailed),
	StatusUnmounting:   setOf(StatusAbsent, StatusMounted, StatusDiscovered, StatusConverting, StatusMounting, StatusMountFailed),
	StatusAbsent:       setOf(StatusDiscovered),
}

// ACTIVE_STATUSES are statuses where the archive is expected present on disk.
var ACTIVE_STATUSES = map[string]struct{}{
	StatusDiscovered:   {},
	StatusConverting:   {},
	StatusIndexing:     {},
	StatusIndexFailed:  {},
	StatusMounting:     {},
	StatusMountFailed:  {},
	StatusMounted:      {},
	StatusHooksRunning: {},
	StatusUnmounting:   {},
}

// PID_STATUSES are statuses that hold or expect a live ratarmount PID.
var PID_STATUSES = map[string]struct{}{
	StatusIndexing:     {},
	StatusMounting:     {},
	StatusMounted:      {},
	StatusHooksRunning: {},
}

// Columns that may be updated alongside a status transition / field patch.
var updatableFields = map[string]struct{}{
	"source_dir":                {},
	"archive_path":              {},
	"archive_basename":          {},
	"size_bytes":                {},
	"mtime_ns":                  {},
	"fingerprint":               {},
	"index_path":                {},
	"overlay_path":              {},
	"mount_path":                {},
	"mount_retryable":           {},
	"mount_attempts":            {},
	"last_seen_at":              {},
	"removed_at":                {},
	"first_mounted_at":          {},
	"hooks_status":              {},
	"hooks_completed_at":        {},
	"last_error":                {},
	"mount_pid":                 {},
	"index_started_at":          {},
	"index_duration_seconds":    {},
	"mount_duration_seconds":    {},
	"convert_source_size_bytes": {},
	"convert_duration_seconds":  {},
}

func setOf(vals ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		m[v] = struct{}{}
	}
	return m
}

// ValidateTransition raises TransitionError if fromStatus → toStatus is not allowed.
// Same → same is always allowed (field-only patch).
func ValidateTransition(fromStatus, toStatus string) error {
	if _, ok := ARCHIVE_STATUSES[fromStatus]; !ok {
		return transitionErrorf("unknown from_status %q", fromStatus)
	}
	if _, ok := ARCHIVE_STATUSES[toStatus]; !ok {
		return transitionErrorf("unknown to_status %q", toStatus)
	}
	if fromStatus == toStatus {
		return nil
	}
	allowed := ALLOWED_TRANSITIONS[fromStatus]
	if _, ok := allowed[toStatus]; !ok {
		list := sortedKeys(allowed)
		if len(list) == 0 {
			return transitionErrorf("illegal transition %q → %q; allowed: none", fromStatus, toStatus)
		}
		return transitionErrorf("illegal transition %q → %q; allowed: %v", fromStatus, toStatus, list)
	}
	return nil
}

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ActiveStatusesList returns ACTIVE_STATUSES as a sorted slice (stable for SQL IN).
func ActiveStatusesList() []string {
	return sortedKeys(ACTIVE_STATUSES)
}

// IsArchiveStatus reports whether s is a known archive status.
func IsArchiveStatus(s string) bool {
	_, ok := ARCHIVE_STATUSES[s]
	return ok
}
