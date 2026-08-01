// Package status builds rich status payloads for the SPA and control plane.
//
// Parity with tarmount-wsl status.py: counts by lifecycle status, per-archive
// progress (elapsed_s, progress_label, source_fs, pid_alive), compact
// indexing_archives, errors_recent, optional metrics merge (include_sizes),
// and a human-readable formatter for CLI.
//
// Build is pure given Options (archives, clock, pid-alive, free-bytes, live
// mounts, metrics). The service layer supplies store rows and live engine state.
package status
