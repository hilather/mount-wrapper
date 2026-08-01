# mount-wrapper

Go rewrite of the [tarmount-wsl](https://github.com/mbrewer/tarmount-wsl) archive auto-mounter orchestrator, with a TypeScript SPA operator dashboard.

**Status:** Feature-complete v1 rewrite. Multi-arch releases (Linux Ubuntu/Rocky + macOS) via GoReleaser on `v*` tags — see [docs/install.md](./docs/install.md). Changelog: [CHANGELOG.md](./CHANGELOG.md). Cut a release: [docs/release.md](./docs/release.md). Plan: [TODO.md](./TODO.md), [migration](./docs/migration.md), [parity](./docs/parity.md).

## Target stack

| Layer | Choice |
|-------|--------|
| Module | `github.com/hilather/mount-wrapper` |
| Daemon / CLI | Go (`mount-wrapper`) |
| Dashboard | TypeScript SPA (Svelte 5), embedded in `serve` |
| Live updates | SSE + poll fallback |
| Mount engines | External `ratarmount-rs` only |
| Service user | `mount-wrapper` |
| Paths | `/etc/mount-wrapper`, `/var/lib/mount-wrapper`, `/run/mount-wrapper` |
| Platforms | Ubuntu (WSL2 primary), Rocky 8, macOS (macFUSE) |
| License | MIT |

## Quick start (dev)

```bash
# Go
make test
make build
./bin/mount-wrapper version
./bin/mount-wrapper help

# Offline diagnostics / config (no serve required)
# ./bin/mount-wrapper doctor --config packaging/examples/config.yaml.example --json
# ./bin/mount-wrapper config show --local --config packaging/examples/config.yaml.example

# Single-tick serve (no long-running loop; useful for smoke without FUSE load)
# ./bin/mount-wrapper serve --config packaging/examples/config.debug.yaml.example --once

# Socket-backed ops (require a running serve with control_socket)
# ./bin/mount-wrapper status --config /path/to/config.yaml          # human
# ./bin/mount-wrapper status --sizes                                # human + sizes appendix
# ./bin/mount-wrapper status --sizes --json                         # full JSON with metrics
# ./bin/mount-wrapper rescan --assume-stable
# ./bin/mount-wrapper metrics                                       # human summary + per-archive
# ./bin/mount-wrapper metrics --json                                # control JSON payload
# ./bin/mount-wrapper mount /path/to/archive.tar
# ./bin/mount-wrapper unmount --all
# ./bin/mount-wrapper purge ARCHIVE_ID --yes
# ./bin/mount-wrapper hooks list
# ./bin/mount-wrapper hooks rerun ARCHIVE_ID --force
# ./bin/mount-wrapper reload              # human: reload scheduled
# ./bin/mount-wrapper reload --json       # machine: {"reload":"scheduled"}
# ./bin/mount-wrapper stop                # human: stop scheduled
# ./bin/mount-wrapper stop --json         # machine: {"stop":"scheduled"}

# SPA (HMR; proxies /api → :8787 when daemon is up)
make web-install
make web-dev

# Embed production SPA into the binary
make web-build
make build
```

## Layout

```text
cmd/mount-wrapper/     CLI + serve entry
internal/config/       YAML load/validate, public snapshot, hot vs restart keys
internal/platform/     Host/WSL detect, FUSE unmount, peer credentials
internal/paths/        WSL/DrvFs mapping, mount name sanitize, service dirs
internal/state/        SQLite lifecycle store + migrations 001–006
internal/match/        Archive name regex + extension allow-list
internal/scanner/      Discovery, stable-file gate, fingerprint, inotify hint
internal/archives/     Relocate to archives_dir + free-space gate
internal/mounter/      Backend resolve, Engine (begin/check/progress/unmount/convert)
internal/convert/      archiveconverter/7z/zip predicates + cmd + metadata
internal/reconcile/    PID/ismount liveness, boot remount plan
internal/cleaner/      Grace purge, overlay quarantine, nonsolid cache hygiene, temp prune
internal/hooks/        hooks.d discovery, security, MOUNT_WRAPPER_* env, runner
internal/metrics/      Space-saved formulas, size providers, collector cache
internal/doctor/       Offline diagnostics report (injectable probes)
internal/control/      Unix socket JSON-lines server/client, peercred auth
internal/service/      Serve loop, pidfile, signals, control + optional HTTP
internal/api/          HTTP API + SSE + SPA static (web_enabled)
internal/cli/          Operator CLI (offline + socket-backed commands)
internal/status/       Rich status payload + human formatter
internal/webui/        embed.FS of SPA dist/
internal/testutil/     Shared test helpers (short Unix socket paths on macOS)
web/                   Svelte 5 + Vite source
packaging/             systemd, launchd, examples, create-user, WSL samples
docs/                  architecture, dev, install, macOS, migration, parity, security
tools/parity/          Offline CLI/config/socket inventories vs tarmount-wsl
packaging/man/         man page (`mount-wrapper.1`)
testdata/              fixtures
```

## Implemented so far

### Config & platform (Phase 1)

- Schema `version: 1`; full key inventory in [TODO.md Appendix D](./TODO.md)
- Load/validate: `internal/config` (`Load`, `LoadText`, `FromMap`, atomic write, patch + dry-run)
- Hot-reload vs restart-required classification (`HotReloadKeys` / `RestartRequiredKeys`); `log_level` + inotify re-sync on reload; `web_enabled`/`web_token` restart-required
- Examples: `packaging/examples/config.yaml.example`, `config.yaml.macos.example`, `config.debug.yaml.example`
- Escape hatch for broken peercred: env `MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH` (default off)

### State machine (Phase 2)

- `internal/state`: pure-Go SQLite (`modernc.org/sqlite`), forward migrations through schema v6
- Status enum + `ALLOWED_TRANSITIONS`, optimistic claim helpers (`ClaimIndexing` / `ClaimConverting` / `ClaimMounting`)
- Hooks rows, purge (DELETE + CASCADE), content-change reset/keep-hooks, meta key/value
- Single-writer rule for production: only `serve` writes the DB

### Scanner, match, relocate (Phase 3 library)

- `internal/match`: `name_regex` compile, extension allow-list, `MatchesArchiveName` / `FilterArchiveNames`
- `internal/scanner`: multi-source walk, stable-file gate, fingerprint, insert/touch/reappear/content-change/absent, `assume_stable`, Linux inotify hint
- `internal/archives`: path classification, `ShouldRelocate` / `RelocateArchive` / free-space checks / convert sidecar move

### Mounter (Phase 4 library + Phase 8 Engine)

- `internal/mounter`: backend normalize/resolve, ratarmount command + child env, process-group start/kill, unmount sequence, partial-index cleanup, concurrent limits, mount-attempt helpers, live registry
- **`Engine`**: `BeginMount`, `CheckChild`, `CompleteIndexAndStartMount`, `MarkMounted`/`MarkFailed`, `ProgressLive`, convert/relocate poll, `Unmount`; injectable `StartProcess` / `IsMount` for tests

### Convert (Phase 5 library + Engine runners)

- `internal/convert`: archiveconverter resolve + argv, 7z nonsolid/flatten helpers + best-effort `7z l -slt` solid/nested/encrypted probe, outer nonsolid cache populate (`EnsureNonsolidCachedCopy`), zip→7z repack predicates/cmd, convert metadata R/W, free-space gates, concurrent convert limits, process runners
- Engine runs convert jobs async (archiveconverter → zip repack → flatten); outer/all scope ensures solid→non-solid cache at mount; service wires default flatten probe when `convert_7z_nonsolid` + scope `flatten`

### Reconcile, cleaner, hooks, metrics (Phase 6 libraries)

- `internal/reconcile`: status-aware PID/ismount liveness, boot remount plan, partial-index cleanup; injectable probes
- `internal/cleaner`: grace purge, overlay quarantine/delete/retain, quarantine prune, outer nonsolid cache hygiene (`cleanup_after`), admin purge, path-safe FS cleanup, boot orphan `/tmp/.tmp*` prune via `DefaultPathInUse`
- `internal/hooks`: discover `hooks.d`, security, `MOUNT_WRAPPER_*` env only, exit 0/75/timeout, sequential/parallel runner
- `internal/metrics`: space-saved formulas, index/mount size providers, summary aggregates, TTL cache collector

### Control plane (Phase 7.1)

- `internal/control`: newline-delimited JSON protocol (`v`/`op`), `Server` + `Client`, peercred auth (root or group `mount-wrapper`)
- Auth escape hatch: `--allow-unauth` or env `MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH=1` (default deny)
- Socket path from config `control_socket` (default `/run/mount-wrapper/control.sock`); mode `0660`; best-effort chown to `mount-wrapper:mount-wrapper`
- Ops via `HandleRequest`: status, metrics, config_get/config_set, rescan, retry, mount, unmount, purge, stop, reload, hooks_*

### Service loop (Phase 8)

- `internal/service`: `Start` / `Run` / `Tick` / `Shutdown`, pidfile flock, SIGTERM/SIGINT/SIGHUP, boot remount, scan/reconcile/clean/progress/work/hooks; `NotifyChange` for SSE push wake
- Control socket bound at start when `control_socket` set; each tick calls `ServeReady`; handler is `HandleRequest`
- CLI (Phase 7.2): full operator surface — offline `doctor`, `config show --local`, `config set` (dry-run / offline write; socket when serve up); socket-backed `status`, `metrics`, `rescan`, `retry`, `mount`, `unmount`, `purge`, `hooks`, `reload`, `stop`; `serve`; platform default config path (Linux `/etc/…`, macOS Application Support)
- Reload: `log_level` → slog (`MOUNT_WRAPPER_LOG_LEVEL` env override); inotify re-sync on `use_inotify`/`source_dirs`; `web_enabled`/`web_token`/`web_host`/`web_port` require process restart
- Socket client: `control.Client` (JSON-lines) used by CLI socket-backed commands

### Doctor (Phase 8 library + CLI)

- `internal/doctor`: offline report (FUSE, bins, paths, disk, config); text/JSON formatters; optional systemd drop-in
- CLI: `mount-wrapper doctor [--json] [--fix-systemd] [--dry-run] [--config PATH]`

### HTTP API + SSE (Phase 9)

- `internal/api`: localhost REST + SSE for the operator SPA; optional Bearer `web_token`; rate-limits on destructive POSTs (purge / unmount-all / rescan)
- Hardening notes: [docs/security.md](./docs/security.md); man page sketch `packaging/man/mount-wrapper.1`
- Embedded in `serve` when `web_enabled: true` (default bind `127.0.0.1:8787`)
- Endpoints: `/api/health`, `/status`, `/archives`, `/metrics`, `/config`, actions (`rescan`/`unmount`/`retry`/`purge`), `/doctor`, `/wsl-info`, SSE `/api/events`; **Prometheus** `GET /metrics` (open on loopback bind)
- SSE events: initial `snapshot`; deltas `counts` / `archive` / `scan` / `low_disk` / optional `metrics`; periodic full `snapshot` resync; `heartbeat` (see [docs/architecture.md](./docs/architecture.md))
- Static SPA from `internal/webui` at `/` (client routes fall back to `index.html`)
- Dev: Vite proxies `/api` → daemon (`make web-dev`); see [docs/dev.md](./docs/dev.md)

### Operator SPA (Phase 10)

- Svelte 5 + TypeScript + Vite under [`web/`](./web/)
- **Archives:** overview pills, savings bar, parity table (status/progress, convert metrics, space saved), filter/sort, row actions (copy/retry/unmount/purge/hooks), Rescan / Unmount all / Doctor, WSL UNC hint
- **Hooks drawer:** per-archive hooks status via `GET /api/hooks?archive_id=` (name/status/attempts/exit); **Re-run** / **Force re-run** via `POST /api/hooks`; focus trap + Escape
- **Settings:** grouped public config form, Validate (dry-run) / Apply, hot-reload vs restart-required banners
- **Live:** SSE client (`/api/events`) with exponential backoff + 15s poll fallback; connection badge (`live (SSE)` / `poll (SSE down)`)
- Auth: `window.__MOUNT_WRAPPER_TOKEN__` (injected by Go) → `Authorization: Bearer` (SSE uses `?token=`)
- Theme: light/dark, persisted as `mw-theme`
- Checks: `make web-check` (svelte-check), `make web-test` (vitest formatters/table/SSE/hooks helpers)
- Typed API surface: hand-written TS (`web/src/lib/api-types.ts` + `types.ts`, D11) — not OpenAPI-generated
- Optional E2E: `cd web && npm run test:e2e:install && RUN_E2E=1 npm run test:e2e` (or `make web-e2e`) — Playwright mocks `/api/*` (Archives shell + Settings validate/apply), no daemon; not in default CI

**Still pending:** OpenAPI-generated SPA client (hand-written types remain); full Phase 11 package publish CI / Homebrew tap.

## Packaging & install

| Artifact | Path |
|----------|------|
| systemd unit | [`packaging/systemd/mount-wrapper.service`](./packaging/systemd/mount-wrapper.service) |
| Optional env file | [`packaging/env.example`](./packaging/env.example) → `/etc/mount-wrapper/env` |
| create-user / dirs | [`packaging/scripts/create-user.sh`](./packaging/scripts/create-user.sh) |
| Linux example config | [`packaging/examples/config.yaml.example`](./packaging/examples/config.yaml.example) |
| macOS example + launchd | [`packaging/examples/config.yaml.macos.example`](./packaging/examples/config.yaml.macos.example), [`packaging/launchd/`](./packaging/launchd/) |
| Homebrew formula sketch | [`packaging/homebrew/mount-wrapper.rb.example`](./packaging/homebrew/mount-wrapper.rb.example) + [`scripts/update-homebrew-formula.sh`](./scripts/update-homebrew-formula.sh) (local `brew --formula`; tap residual) |
| WSL | [`packaging/wsl.conf.snippet`](./packaging/wsl.conf.snippet), [`packaging/windows-task-scheduler.xml.example`](./packaging/windows-task-scheduler.xml.example) |
| goreleaser / nfpm | [`.goreleaser.yaml`](./.goreleaser.yaml), [`packaging/nfpm.yaml`](./packaging/nfpm.yaml); publish: [`.github/workflows/release.yml`](./.github/workflows/release.yml) |
| Optional musl/static (D7) | `make build-musl` / `make package-musl` → `*_linux_*_musl.tar.gz`; CI `musl-static-smoke` + release attach |

**Service user:** `mount-wrapper`. **Web:** embedded (`web_enabled`). **Control:** `control_socket` under `/run/mount-wrapper/`.

Version embedding: `make build` uses `-ldflags` for `main.version` / `main.commit` / `main.date` (`mount-wrapper version`). Release checksums: `SHA256SUMS` via goreleaser (`CGO_ENABLED=0` primary) plus optional musl lines after package.

Operator guide: **[docs/install.md](./docs/install.md)** (engines, Rocky glibc, macFUSE, WSL Task Scheduler).

## Docs

- [CHANGELOG.md](./CHANGELOG.md) — Keep a Changelog (v0.1.5 + Unreleased)
- [docs/release.md](./docs/release.md) — how to cut a release (tag, Actions, verify)
- [TODO.md](./TODO.md) — phased rewrite checklist, decisions log, module map
- [docs/dev.md](./docs/dev.md) — local development
- [docs/install.md](./docs/install.md) — install, packaging, engines, WSL/macOS
- [docs/macos.md](./docs/macos.md) — macFUSE, launchd, socket path length
- [docs/architecture.md](./docs/architecture.md) — target architecture
- [AGENTS.md](./AGENTS.md) — **mandatory agent policy** (docs, tests, review)

### Agent / contributor policy (summary)

Every behavior-changing change must, in the same PR:

1. Update documentation (`README`, `TODO`, `docs/`, packaging examples as needed)
2. Add or update regression tests where it makes sense
3. Pass a code review pass (`/review` or structured self-review) before “done”

Details and checklists: [AGENTS.md](./AGENTS.md). Project skills under `.grok/skills/`.
