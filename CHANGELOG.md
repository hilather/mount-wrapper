# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **macOS CI:** control/service unit tests bind Unix sockets under a short
  `/tmp` path on Darwin so `sun_path` (~104 bytes) is not exceeded by long
  GitHub Actions `t.TempDir()` paths (`internal/testutil.ShortUnixSocketPath`).
  CI also sets `TMPDIR=/tmp` and package-level test summary on failure.
- **Doctor (macOS):** `systemd_pid1` no longer suggests `serve --foreground`
  (flag does not exist); points at login-user serve + launchd example.

### Changed

- **Packaging docs/TODO:** mark deb/rpm + postinstall as done on `v*` releases;
  wire standalone `packaging/nfpm.yaml` `scripts.postinstall` to match
  GoReleaser; honest residual is config.yaml seed + Homebrew tap.
- **Drift guard:** `TestSettingsSchemaMatchesPublicKeys` keeps SPA
  `settings-schema.ts` aligned with `config.PublicKeys()`.

## [0.1.1] - 2026-08-01

### Changed

- **Mount backend is `ratarmount-rs` only.** Reject `mount_backend` values
  `python` / `ratarmount` and related aliases. Config defaults, backend
  resolver, doctor checks, optional FUSE smoke, SPA settings schema, and docs
  assume `rust` (`ratarmount-rs`) exclusively. Python-only single-phase /
  in-memory index special cases removed.

### Added

- **Ubuntu + Rocky 8 binary smoke CI** (`.github/workflows/smoke.yml`): build
  and run `scripts/smoke-binary.sh`; Rocky via container
  (`scripts/smoke-rocky8.sh`); optional FUSE job on workflow dispatch.
- **Musl/static path (D7):** `scripts/build-musl.sh`, `make build-musl` /
  `smoke-musl` / `package-musl`; CI musl smoke; **release workflow** attaches
  optional `*_linux_*_musl.tar.gz` after GoReleaser and refreshes `SHA256SUMS`.
  Primary published packages remain pure-Go `CGO_ENABLED=0`.
- **macOS CI:** unit tests + `CGO_ENABLED=0` build + binary smoke
  (`macos-unit-smoke` in `ci.yml`). No macFUSE in default CI.
- **Convert hardening:** encrypted 7z detect (`7z l -slt`), outer nonsolid
  cache populate (`EnsureNonsolidCachedCopy`) and engine wire-up.
- **Nested automount skip summary:** drain ratarmount-rs stderr; enrich
  `last_error` with `skipped N nested mounts: …` on mount failure.
- **Field-test checklist:** `docs/field-test.md`.
- **SPA:** Playwright Settings e2e (`RUN_E2E=1`); shared e2e helpers.
- **Packaging:** Homebrew formula sketch polish.
- **OpenAPI sketch:** `docs/openapi.yaml` (hand-written; SPA still uses TS types).
- **Tests / docs:** ShouldPreconvert matrix; packaging artifact tests;
  CHANGELOG + `docs/release.md`.

### Docs

- Install/dev/parity/architecture notes for smoke matrix, musl optional path,
  macOS CI scope, and ratarmount-rs-only engine policy.

## [0.1.0] - 2026-07-31

First release of the Go rewrite of [tarmount-wsl](https://github.com/mbrewer/tarmount-wsl):
feature-complete orchestrator with multi-arch packaging.

### Added

- **Daemon / CLI (`mount-wrapper`):** config load/validate (YAML schema
  `version: 1`), SQLite lifecycle store, archive discovery/scanner, mounter
  engine (ratarmount-rs child process management), convert runners (7z/zip/
  archiveconverter), hooks, reconcile, cleaner, doctor, metrics.
- **Control plane:** Unix socket JSON-lines server/client with peercred auth;
  operator CLI (offline + socket-backed ops).
- **HTTP / SSE / SPA:** embedded Svelte 5 dashboard, `/api/*`, SSE live updates,
  optional Bearer `web_token`, Prometheus `GET /metrics`.
- **Packaging:** GoReleaser multi-arch (linux/darwin amd64+arm64), deb/rpm,
  systemd/launchd examples, man page sketch.

### Notes

- Engines not bundled: install **ratarmount-rs**, fuse3/macFUSE, optional
  archiveconverter and 7z separately.

[Unreleased]: https://github.com/hilather/mount-wrapper/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.1
[0.1.0]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.0
