package hooks

import (
	"fmt"

	"github.com/hilather/mount-wrapper/internal/state"
)

// ClassifyExit maps a process outcome to a per-hook row status and optional error.
// Status is one of: success, failed, retry (state.Hook*).
func ClassifyExit(returnCode *int, timedOut bool, attempts, maxRetries int) (status string, errMsg string) {
	if timedOut {
		if attempts > maxRetries {
			return state.HookFailed, fmt.Sprintf(
				"timeout (attempts=%d > max_retries=%d)", attempts, maxRetries,
			)
		}
		return state.HookRetry, "timeout"
	}

	if returnCode == nil {
		return state.HookFailed, "no exit code"
	}

	code := *returnCode
	if code == 0 {
		return state.HookSuccess, ""
	}

	if code == ExitRetry {
		if attempts > maxRetries {
			return state.HookFailed, fmt.Sprintf(
				"EX_TEMPFAIL exhausted (attempts=%d > max_retries=%d)", attempts, maxRetries,
			)
		}
		return state.HookRetry, "EX_TEMPFAIL (75)"
	}

	// Negative exit codes often mean killed by signal (-N) in some runtimes;
	// Go's WaitStatus typically exposes via ProcessState.ExitCode() as -1 for
	// signals. Treat non-zero non-75 as hard fail.
	if code < 0 {
		return state.HookFailed, fmt.Sprintf("killed by signal %d", -code)
	}

	return state.HookFailed, fmt.Sprintf("hard fail exit_code=%d", code)
}

// ShouldRunHooks reports whether the archive is eligible for a first-mount
// hook cycle. Terminal success is never re-run. Terminal failed is re-run only
// when rerunOnFailure is true (config hook_rerun_on_failure).
// Eligible: none | pending | retry | running (resume).
func ShouldRunHooks(hooksStatus string, rerunOnFailure bool) bool {
	switch hooksStatus {
	case state.HooksSuccess:
		return false
	case state.HooksFailed:
		return rerunOnFailure
	case state.HooksNone, state.HooksPending, state.HooksRetry, state.HooksRunning:
		return true
	default:
		return false
	}
}

// ShouldRunHooksRecord is ShouldRunHooks for a full archive record.
func ShouldRunHooksRecord(rec *state.ArchiveRecord, rerunOnFailure bool) bool {
	if rec == nil {
		return false
	}
	return ShouldRunHooks(rec.HooksStatus, rerunOnFailure)
}

// IsTerminalHooksStatus reports success or failed aggregate (no automatic re-run
// on remount unless hook_rerun_on_failure for failed).
func IsTerminalHooksStatus(hooksStatus string) bool {
	return hooksStatus == state.HooksSuccess || hooksStatus == state.HooksFailed
}

// AggregateStatus maps individual hook results to archive-level hooks_status.
//
// Rules (parity with Python HookRunner._aggregate_and_finish):
//   - any failed → failed
//   - else any retry → retry
//   - else all success and/or skipped → success (unless only skipped → failed)
//   - empty results → success (no hooks configured)
func AggregateStatus(results []RunResult) string {
	if len(results) == 0 {
		return state.HooksSuccess
	}
	var hasFailed, hasRetry, hasSuccess, hasSkipped bool
	for _, r := range results {
		switch r.Status {
		case state.HookFailed:
			hasFailed = true
		case state.HookRetry:
			hasRetry = true
		case state.HookSuccess:
			hasSuccess = true
		case state.HookSkipped:
			hasSkipped = true
		}
	}
	if hasFailed {
		return state.HooksFailed
	}
	if hasRetry {
		return state.HooksRetry
	}
	if hasSkipped && !hasSuccess {
		return state.HooksFailed
	}
	return state.HooksSuccess
}
