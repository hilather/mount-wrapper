// Package mounter supervises ratarmount / ratarmount-rs child processes.
//
// Phase 4 core library: backend selection, binary resolution, CLI construction,
// live mount registry, process-group helpers, unmount sequence, partial-index
// cleanup, concurrent-limit and mount-attempt helpers, DrvFs index guards, and
// nested-automount stderr drain / skip summaries (ParseNestedMountFailure,
// FormatNestedSkipSummary; Engine pipes child stderr while ratarmount-rs runs).
//
// Phase 8 Engine (engine.go): BeginMount / CheckChild / CompleteIndexAndStartMount /
// MarkMounted / MarkFailed / ProgressLive / PollConvert / PollRelocate / Unmount
// with convert jobs (async: archiveconverter → zip repack → flatten) and relocate
// (sync v1). The service package drives these methods from the tick loop;
// convert workers must not write the store (only PollConvert does).
//
// Windows visibility (windows_visible → FUSE -o allow_other):
//
// Linux/WSL path traversal for \\wsl.localhost\… (or other non-owner users)
// requires every ancestor of mount_root to be other-executable (o+x / mode
// +0111 on dirs). create-user.sh applies o+x to /var/lib/mount-wrapper and
// …/mounts when present; operators must ensure intermediate parents (e.g.
// custom mount_root under a home directory) are also traversable. FUSE
// allow_other still needs user_allow_other in /etc/fuse.conf. mount-wrapper
// does not chmod arbitrary parents at runtime (security: no automatic
// world-x on operator paths). Doctor check windows_visible_parent_ox warns
// when ancestors lack o+x. See docs/architecture.md.
//
// Parity sources: tarmount-wsl mounter.py + backends.py.
package mounter
