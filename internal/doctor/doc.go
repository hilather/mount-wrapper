// Package doctor runs environment diagnostics and optional systemd drop-in fixes.
//
// The library is offline (no serve process required). Callers (CLI doctor,
// HTTP GET /api/doctor) supply a validated *config.Config when available,
// or nil for host/binary-only checks. Live probes (when configured) still
// never hard-fail offline:
//
//   - control_socket_live: short status dial when control_socket is set
//   - pidfile_live: stat + PID parse + process liveness when pid_file is set
//   - systemd_unit: systemctl is-active/is-enabled when PID 1 is systemd
//
// All external probes (PATH lookup, filesystem, free space, user lookup,
// binary --version/--help, fuse.conf, PID 1, systemctl, process liveness,
// control socket dial) are injectable via Options so unit tests never need
// real FUSE, ratarmount, systemd, or a running serve.
package doctor
