// Package doctor runs environment diagnostics and optional systemd drop-in fixes.
//
// The library is offline (no serve process required). Callers (CLI doctor,
// HTTP GET /api/doctor) supply a validated *config.Config when available,
// or nil for host/binary-only checks. When control_socket is set, doctor also
// attempts a short live status dial (control_socket_live); missing serve or
// auth denial is severity warn, never a hard fail.
//
// All external probes (PATH lookup, filesystem, free space, user lookup,
// binary --version/--help, fuse.conf, PID 1, control socket dial) are
// injectable via Options so unit tests never need real FUSE, ratarmount,
// systemd, or a running serve.
package doctor
