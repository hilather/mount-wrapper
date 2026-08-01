// Package reconcile checks mount/PID liveness and repairs archive state.
//
// Status-aware rules (parity with tarmount-wsl reconcile.py / design K):
//
//   - indexing / mounting / converting: never treat “not ismount” alone as
//     failure (long first-time indexes). Fail only on dead PID, supervised
//     process exit, convert-job absence, or mount_ready timeout.
//   - mounted / hooks_running: require both ismount and a live mount PID.
//   - Failures transition to index_failed / mount_failed and clear mount_pid.
//     hooks_status is not reset — terminal success must not re-run on remount
//     (see hooks.ShouldRunHooks).
//
// Boot (PlanBoot / Boot): clear stale PIDs, re-queue in-progress work, force
// remount of previously mounted archives via mount_failed, and optionally
// clean partial indexes.
//
// Serve call site (wired in `internal/service`):
//
//	// on start:
//	reconciler.Boot()                 // clear PIDs + remount plan + apply
//	reconciler.CleanupPartialIndexes()
//	// main loop (every reconcile_interval_seconds via service.Tick):
//	result, err := reconciler.Reconcile()
//
// Probes (ismount, process-alive, path-exists, clock) are injectable for tests.
package reconcile
