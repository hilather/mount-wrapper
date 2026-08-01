// Package doctor runs environment diagnostics and optional systemd drop-in fixes.
//
// The library is offline (no serve process required). Callers (CLI doctor,
// HTTP GET /api/doctor later) supply a validated *config.Config when available,
// or nil for host/binary-only checks.
//
// All external probes (PATH lookup, filesystem, free space, user lookup,
// binary --version/--help, fuse.conf, PID 1) are injectable via Options so unit
// tests never need real FUSE, ratarmount, or systemd.
package doctor
