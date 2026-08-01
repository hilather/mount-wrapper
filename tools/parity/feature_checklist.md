# Feature checklist — tarmount-wsl README → mount-wrapper

Maps the upstream README feature table (and closely related product surfaces) to
**mount-wrapper** status. Update when behavior lands or is deferred.

| Status | Meaning |
|--------|---------|
| **done** | Implemented and used in serve/CLI/API |
| **partial** | Library or main path works; residual gaps called out |
| **deferred** | Intentionally later / out of scope for cutover |
| **n/a** | Design choice removes the surface |

_Last reviewed: 2026-08-01 (post v0.1.1 packaging truth-up)._

## README feature rows

| Area | Upstream (tarmount-wsl) | mount-wrapper | Status | Notes |
|------|-------------------------|---------------|--------|-------|
| **Discovery** | Poll source dirs; optional inotify; stable-file gate | `internal/scanner` + serve tick | **done** | DrvFs-friendly poll; inotify Linux-only hint |
| **Mounting** | ratarmount FUSE + overlay, recursive, `allow_other` | `internal/mounter` Engine; default backend **rust** | **done** | Engines external (not vendored venv); **ratarmount-rs only** (D14) |
| **Lifecycle** | SQLite state, boot remount, reconcile, deferred cleanup, overlay policy | state + reconcile + cleaner | **done** | Same logical schema 001–006; **new DB path** (D5) |
| **Hooks** | First-mount `hooks.d`, retries, hard-fail | `internal/hooks` + serve after first mount | **done** | Env **`MOUNT_WRAPPER_*` only** (D3) |
| **Control plane** | Unix socket API for CLI/web | `internal/control` + `Service.HandleRequest` | **done** | **New protocol only** (D6); op names familiar |
| **Metrics** | Archive/index/extracted sizes, space saved | `internal/metrics` + status/API | **done** | Convert duration/size fields included |
| **Config** | YAML validate; hot vs restart; live apply | `internal/config` + CLI/API | **done** | Path defaults under `/…/mount-wrapper` |
| **Web UI** | Localhost dashboard (vanilla) | Embedded Svelte 5 SPA + SSE | **done** | No sidecar web unit (D4) |
| **Packaging** | `.deb`, private ratarmount venv, systemd, doctor | deb/rpm + systemd/launchd; doctor | **done** | No vendored Python venv; publish on `v*` (`release.yml`); first-install `config.yaml` seed via `seed-config.sh`; residual: no Homebrew tap |

## Extended product surfaces (not always in README table)

| Area | Status | Notes |
|------|--------|-------|
| Convert: archiveconverter | **done** | Engine `PollConvert` / async jobs |
| Convert: built-in 7z nonsolid / flatten | **partial** | Predicates + runners + CLI encrypted detect + outer cache populate (fail-closed list, size floor, flock); no stream-flatten / full solid-folder parse |
| Convert: zip→7z repack | **partial** | Library + runners; `testdata/nestedzip` + real-`7z` `RunZipRepack` when on PATH; engine/serve residual |
| Doctor | **done** | Offline CLI + `GET /api/doctor` |
| WSL UNC hint | **done** | API + SPA |
| Rocky 8 install path | **done** | Documented + rocky CI smoke; optional musl path (`build-musl` / `package-musl`); release attaches `*_musl.tar.gz` (D7) |
| macOS macFUSE | **partial** | Platform code + docs; friend-test residual |
| SPA hooks detail drawer | **done** | `HooksDrawer` + `GET /api/hooks` + `POST /api/hooks` re-run/force |
| Real FUSE integration tests | **deferred** | Unit suite offline; optional build-tag later |
| Prometheus metrics | **done** | `GET /metrics` text exposition; loopback open scrape |
| Dual install with Python package | **n/a** | Soft replace (D13); see migration guide |

## How to refresh scripted inventories

```bash
# from mount-wrapper repo root
./tools/parity/run_all.sh
# outputs: tools/parity/{cli_surface,config_keys,socket_ops}.md
# feature_checklist.md is maintained manually (this file)
```

See [docs/parity.md](../../docs/parity.md) for residual gaps and platform manual checklists.
