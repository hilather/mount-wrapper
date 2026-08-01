# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Changes on `main` since **v0.1.0** (intended for **v0.1.1**).

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
- **Musl/static smoke path (D7):** `scripts/build-musl.sh`,
  `make build-musl` / `make smoke-musl`, CI job `musl-static-smoke` (Alpine).
  Primary published releases remain `CGO_ENABLED=0` pure-Go (not a second
  goreleaser musl matrix).
- **macOS CI:** unit tests + `CGO_ENABLED=0` build + binary smoke
  (`macos-unit-smoke` in `ci.yml`). No macFUSE in default CI.
- **Convert hardening:** encrypted 7z member detect (`7z l -slt` probe),
  outer nonsolid cache populate (`EnsureNonsolidCachedCopy`) and engine
  wire-up for solid→non-solid outer/all scope paths.
- **Field-test checklist:** `docs/field-test.md` for v0.1.x install
  verification (smoke, real mount, convert, web).
- **SPA:** Playwright Settings e2e (`RUN_E2E=1` / `make web-e2e`); shared e2e
  helpers.
- **Packaging:** Homebrew formula sketch polish
  (`packaging/homebrew/mount-wrapper.rb.example`).
- **Tests:** `ShouldPreconvert` matrix coverage; convert outer-cache and solid
  probe unit tests; packaging artifact path tests.

### Docs

- Install/dev/parity/architecture notes for smoke matrix, musl optional path,
  macOS CI scope, and ratarmount-rs-only engine policy.

## [0.1.0] - 2026-07-31

First release of the Go rewrite of [tarmount-wsl](https://github.com/mbrewer/tarmount-wsl):
feature-complete orchestrator with multi-arch packaging.

### Added

- **Daemon / CLI (`mount-wrapper`):** config load/validate (YAML schema
  `version: 1`), SQLite lifecycle store, archive discovery/scanner, mounter
  engine (ratarmount child process management), convert runners (7z/zip/
  archiveconverter), hooks, reconcile, cleaner, doctor, metrics.
- **Control plane:** Unix socket JSON-lines server/client with peercred auth;
  operator CLI (offline + socket-backed ops).
- **HTTP API + SSE:** REST + Server-Sent Events, optional Bearer token, SPA
  static embed (`web_enabled`), Prometheus `/metrics`.
- **SPA:** Svelte 5 operator dashboard (archives, status, doctor, hooks
  drawer, settings validate/apply, SSE + poll fallback).
- **Packaging sketches:** systemd unit, launchd example, create-user script,
  config examples (Linux/macOS/debug), WSL snippets, nfpm, goreleaser
  (`CGO_ENABLED=0`), man page.
- **Release matrix (tag `v*`):** linux/darwin × amd64/arm64 tarballs, deb,
  rpm, `SHA256SUMS` via GoReleaser (`.github/workflows/release.yml`).
- **CI:** Ubuntu unit tests + race subset + build + web checks
  (`.github/workflows/ci.yml`).
- **Docs:** architecture, install, dev, migration, parity, security, macOS.

[Unreleased]: https://github.com/hilather/mount-wrapper/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.0
