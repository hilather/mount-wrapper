# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Doctor **`--fix-systemd --dry-run`**: preview generated systemd drop-in unit
  text (report notes + JSON `details.content`) without writing the drop-in.
- **SPA Settings:** sticky process-restart banner when Apply returns non-empty
  `restart_required` (e.g. `web_token` / other `web_*`). Survives Validate and
  Reload until Dismiss; optional `sessionStorage`. Does **not** live-apply
  `web_token` (serve-start capture unchanged). Playwright e2e coverage.

### Fixed

- `metrics.Cache` concurrent safety: `sync.RWMutex` around the dual-key
  `(archive_id, prefer_mount)` entries map (`Get` / `Put` / `Invalidate`).

## [0.1.3] - 2026-08-01

Operator polish and correctness after v0.1.2. Mount backend remains
**ratarmount-rs only**.

### Added

- Nested automount skip visibility on **mounted** rows (`nested_skips_count` /
  `nested_skips_summary` + SPA warning chip).
- SPA SSE fine-grained events: `archive` / `scan` / `low_disk` / `metrics`
  (row patch without full table wipe; metrics preserved on partial status).
- Hot-reload: `log_level` → slog at serve + reload; `MOUNT_WRAPPER_LOG_LEVEL`
  override; inotify re-sync; CLI **`reload`** (`reload scheduled`).
- Doctor `windows_visible_parent_ox` (Linux UNC `o+x` parent walk).

### Fixed

- Nested-skip advisory durability across **index→mount** and **remount**.
- Metrics cache dual-keyed by `(archive_id, prefer_mount)` (no stale index
  sizes for `--prefer-mount`).

### Changed

- Doctor `--fix-systemd` drop-in includes custom absolute data roots
  (`mount_root`, `index_dir`, `overlay_dir`, convert cache / AC output) as
  `ReadWritePaths` (deduped; sources still RO).
- `web_enabled` / `web_token` **restart-required** (honest bind/token model).
- Man EXIT STATUS **0 / 1 / 2 / 4 / 5**.

## [0.1.2] - 2026-08-01

Patch after v0.1.1: convert outer-cache production path, packaging inventory,
doctor contracts, SPA e2e, and operator install polish. Mount backend remains
**ratarmount-rs only**.

### Added

#### Convert / metrics
- Outer nonsolid cache **flock** on `{cacheKey}.lock` (concurrent populate safe).
- Convert **metrics sidecar** wiring (`ConvertSidecarMeta`) + store convert
  columns on mount claim when cache path differs from source.
- Outer-cache **edge hardening** (fail-closed `7z l`, size floor, partial
  cleanup, encrypted extract messaging).
- Cleaner **nonsolid cache hygiene** (partials, stale locks, age prune via
  `cleanup_after`).

#### Packaging / install
- First-install **config seed** (`seed-config.sh`, never overwrites).
- Deb package content smoke (`scripts/smoke-package-contents.sh`, CI job) +
  always-on **tar member inventory** fixture under `make test`.
- Homebrew formula sketch **0.1.2** + `scripts/update-homebrew-formula.sh`
  (tap publish still residual).

#### Doctor / API / SPA
- Doctor probes: `web_bind_security`, `convert_cache_dir`, Darwin
  `control_socket_path_length`; check-name inventory freeze; JSON structural
  golden; SPA `DoctorReport` types tightened.
- Hand-written OpenAPI **0.1.3** with response schemas (codegen residual).
- Playwright e2e: archives actions + doctor panel (`RUN_E2E=1`).

### Fixed

- macOS CI: bash 3.2 parity script parse; short Unix socket test paths;
  doctor no longer suggests `serve --foreground`.

### Changed

- Packaging docs mark deb/rpm + postinstall + config seed done on `v*` tags.
- SPA settings schema drift guard vs `config.PublicKeys()`.

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

[Unreleased]: https://github.com/hilather/mount-wrapper/compare/v0.1.3...HEAD
[0.1.3]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.3
[0.1.2]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.2
[0.1.1]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.1
[0.1.0]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.0
