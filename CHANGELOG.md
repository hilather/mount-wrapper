# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Doctor `windows_visible_parent_ox`:** when config is loaded and
  `windows_visible` is true on Linux, walks `mount_root` ancestors and **warns**
  if any existing directory lacks other-execute (`o+x`); message/details include
  a `chmod o+x …` fix hint. macOS and `windows_visible: false` are info-only.
  Inventory freeze + temp-dir tests (0700 vs 0755).
- **Nested automount skips on mounted rows:** when ratarmount-rs skips nested
  members during a successful mount, status/API expose `nested_skips_count` +
  `nested_skips_summary` (from live engine state and/or `last_error` summary).
  SPA Archives shows a warning chip and subtitle; failed rows still enrich
  `last_error`. No SQLite migration — pure skip summary may be stored in
  `last_error` on mounted success; hooks success preserves that advisory.
- **SPA SSE fine-grained events:** client handles `archive` / `scan` / `low_disk` /
  `metrics` in addition to `snapshot` / `counts` / `heartbeat`. Archive events
  patch or remove rows by `archive_id` without a full table wipe; merge helpers
  preserve per-row size metrics when status patches omit them.
- **Hot-reload / logging apply:** `log_level` is applied to slog at `serve`
  start and on every config reload; `MOUNT_WRAPPER_LOG_LEVEL` overrides config
  while set (documented in `packaging/env.example` and the man page).
- **Inotify re-sync on reload:** `use_inotify` / mapped `source_dirs` changes
  restart or stop the Linux inotify watcher (poll remains authoritative).
- **CLI `reload`:** socket-backed equivalent of control `reload` / SIGHUP;
  prints a human success line (`reload scheduled`) on exit 0.

### Changed

- **Nested-skip advisory durability:** `CompleteIndexAndStartMount` persists a
  pure skip summary into `last_error` and carries `SkippedNested` onto the
  FUSE-phase live mount; `MarkMounted` keeps an existing pure nested-skip
  `last_error` when live skips are empty (index→mount and remount paths).
- **`web_enabled` / `web_token` are restart-required** (HTTP bind and Bearer
  token are fixed at serve start). SPA settings schema marks them as restart;
  API `hot_reload_keys` / `restart_required_keys` updated.
- **Man page EXIT STATUS:** documents process exit codes **0 / 1 / 2 / 4 / 5**
  (success, general error, usage, service unavailable, permission denied)
  matching CLI helpers.
- **Operator docs for next patch:** `docs/field-test.md` retargeted to v0.1.3
  (reload, `log_level` hot-reload, SSE deltas, nested skip remount checks);
  `docs/release.md` examples aim at **v0.1.3**.

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

[Unreleased]: https://github.com/hilather/mount-wrapper/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.2
[0.1.1]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.1
[0.1.0]: https://github.com/hilather/mount-wrapper/releases/tag/v0.1.0
