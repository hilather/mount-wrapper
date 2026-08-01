// Package hooks discovers and runs first-mount scripts (MOUNT_WRAPPER_* protocol).
//
// Hooks are language-agnostic executables under hooks.d. Discovery ignores samples
// and disabled names; security checks refuse group/other-writable files and path
// escape via symlink. Exit codes: 0 success, 75 (EX_TEMPFAIL) retryable, other
// hard fail; supervisor timeout is retryable. Aggregate archive hooks_status is
// none|pending|running|success|failed|retry (see state.Hooks*).
//
// Terminal success/failed archives are not re-run on remount unless force or
// (for failed only) config hook_rerun_on_failure. This package is a library; the
// serve loop / CLI wire it in later phases.
package hooks
