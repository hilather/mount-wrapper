# mount-wrapper rewrite TODO

Go daemon + TypeScript SPA rewrite of [tarmount-wsl](https://github.com/mbrewer/tarmount-wsl) feature parity.

**Target stack**
- **Backend:** Go (single binary orchestrator; shells out to ratarmount / ratarmount-rs / archiveconverter)
- **Frontend:** TypeScript SPA (Svelte recommended; Vue acceptable)
- **Platforms:** Ubuntu (WSL2 primary), Rocky 8, macOS (macFUSE)
- **Source of truth for behavior:** `tarmount-wsl` design + implementation

**Architecture (target)**

```text
SPA (TS) ──HTTP/SSE──► Go serve (SQLite, scanner, mounter, hooks, cleaner)
CLI ──────UDS JSON────► same process
                          │ exec
                    ratarmount(-rs) / archiveconverter / 7z tools
```

---

## Legend

- `[ ]` pending
- `[~]` in progress
- `[x]` done
- `[!]` blocked / needs decision

---

## Phase 0 — Project bootstrap

- [x] Initialize Go module (`github.com/hilather/mount-wrapper` — D12)
- [x] Binary & package name: `mount-wrapper` only (D2; no `tarmount-wsl` alias)
- [x] Repo layout:
  - [x] `cmd/mount-wrapper/` — CLI + serve entry
  - [x] `internal/` — packages (config, state, scanner, mounter, …)
  - [x] `web/` — TypeScript SPA source
  - [x] `packaging/` — systemd, deb, rpm, launchd, examples
  - [x] `docs/` — design notes, platform notes
  - [x] `testdata/` — fixtures (nested 7z, etc.)
- [x] Tooling: `go.mod`, `Makefile`, `.golangci.yml`, `go test ./...`
- [x] Frontend scaffold (Vite + Svelte 5 + TypeScript) under `web/`
- [x] Embed SPA build into Go binary via `embed.FS` (dev: Vite proxy to Go)
- [x] CI matrix sketch: Ubuntu unit tests; later Rocky 8 + macOS
- [x] License (MIT to match upstream) + root README (status: rewrite in progress)
- [x] Map tarmount-wsl modules → Go packages (see Appendix A)

---

## Phase 1 — Config & platform foundations

### 1.1 Config

- [x] YAML config load/validate (`version: 1`)
- [x] Duration parsing (`24h`, `168h`, seconds) + format back for snapshot
- [x] **Full public key inventory** (Appendix D) — every upstream `Config` field + aliases
- [x] Public config snapshot for API/CLI (`config show`) including:
  - [x] values, `hot_reload_keys`, `restart_required_keys`, `config_path`, unknown keys
- [x] Patch merge + dry-run validate (`strict_config` unknown-key policy)
- [x] Hot-reload vs restart-required classification (copy upstream `HOT_RELOAD_KEYS` / `RESTART_REQUIRED_KEYS`)
- [x] Config write-back (atomic replace)
- [x] Default path profiles:
  - [x] Linux packaged (`/var/lib/...`, `/run/...`, `/etc/...`) — `internal/config` + `internal/platform`
  - [x] macOS user (`~/Library/Application Support/...`, Caches run dir) — `internal/platform`
- [x] Example configs: Linux/WSL, macOS, debug
- [x] Legacy aliases: `stage_archive_to` → `archives_dir`; duration dual keys (`cleanup_after` / `cleanup_after_seconds`)

### 1.2 Platform support

- [x] Host detect: `linux` / `darwin` / other
- [x] WSL detection (`WSL_DISTRO_NAME`, `/proc/version`)
- [x] FUSE device probes
- [x] Unmount: Linux `fusermount3`/`fusermount`; Darwin `umount` / `diskutil unmount`
- [x] Peer credentials: `SO_PEERCRED` (Linux); `LOCAL_PEERCRED` / xucred (Darwin; pid may be -1)
- [x] Optional unauth escape hatch for broken peercred (`MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH`)
- [x] Path helpers:
  - [x] Windows drive letter → `/mnt/<letter>/...`
  - [x] Reject `\\wsl.localhost` / `\\wsl$` UNC as source config
  - [x] DrvFs detection (`/mnt/[a-z]`)
  - [x] Mount name sanitization + collision suffix
- [x] Service directory creation + modes

### 1.3 Tests (phase 1)

- [x] Config unit tests (valid/invalid, hot vs restart keys)
- [x] Path mapping unit tests
- [x] Platform unmount command selection (mocked)
- [x] Peercred backend selection (mocked)

---

## Phase 2 — SQLite state machine

- [x] Schema + forward migrations (parity with tarmount-wsl migrations 001–006+)
- [x] Tables: `schema_version`, `archives`, `hooks`, `meta`
- [x] Archive status enum + allowed transitions (**first-class; not optional**):
  - [x] `discovered` | `converting` | `indexing` | `index_failed`
  - [x] `mounting` | `mount_failed` | `mounted` | `hooks_running`
  - [x] `unmounting` | `absent`
  - [x] Encode full `ALLOWED_TRANSITIONS` table from Python `state.py` (incl. `converting` edges)
- [x] Single-writer rule: only `serve` writes DB (documented in `internal/state`; `Store` MaxOpenConns=1)
- [x] CRUD + claim work (`UPDATE … WHERE status=?` optimistic lock)
  - [x] claim convert / claim index / claim mount helpers
- [x] Fingerprint **storage** + `RecordContentChange` (reset vs keep hooks); compute hash lives in Phase 3 scanner
- [x] Hooks fields: `hooks_status` (`none|pending|running|success|failed|retry`), `hooks_completed_at`, per-hook rows
- [x] Duration / size fields (parity migrations 002–006):
  - [x] `index_started_at`, `index_duration_seconds`, `mount_duration_seconds`
  - [x] `convert_source_size_bytes`, `convert_duration_seconds`
- [x] Mount fields: `mount_pid`, `mount_attempts`, `mount_retryable`, paths, `last_error`
- [x] Purge = `DELETE` row (CASCADE hooks); no durable `purged` status
- [x] WAL + foreign keys; CHECK constraints evolve with migrations (`converting` in status CHECK)

### Tests

- [x] Migration apply from empty DB (through latest)
- [x] Status transitions / claim races (incl. converting)
- [x] Purge cascade
- [x] Fingerprint change behavior (`RecordContentChange` reset vs keep hooks)

---

## Phase 3 — Scanner & discovery

- [x] Poll loop (`poll_interval_seconds`, default 60) — `service.Tick` + `serve`
- [x] Multi `source_dirs` walk (`recursive` flag) via `internal/scanner`
- [x] `name_regex` helpers (`internal/match`) + scanner wiring
- [x] Stable-file gate: `two_scans` | `min_age` | `both` (+ `min_file_age_seconds`)
- [x] `content_fingerprint` toggle (size:mtime vs + content samples)
- [x] New path → insert `discovered`
- [x] Same path fingerprint change → `on_content_change`:
  - [x] `remount_reset_hooks` (default) | `remount_keep_hooks`
- [x] Missing files → `unmounting` → `absent` + `removed_at`
- [x] Optional inotify (Linux only; skip on Darwin; never on DrvFs) — hint only; poll authoritative
- [x] `max_archive_bytes` gate
- [x] Optional relocate archives to Linux FS (`move_archives_to_linux` / `archives_dir`)
  - [x] Path classification: `IsArchivesPath` / `IsConvertedOutputPath` (`internal/archives`)
  - [x] Accept legacy alias `stage_archive_to` → `archives_dir` (config load; Phase 1)
  - [x] Relocate move + collision suffix + sidecar (`internal/archives`; Engine sync relocate)
  - [x] `archive_relocate_overhead_bytes` free-space headroom
- [x] Free-space checks before relocate (`min_free_bytes` + overhead; `CheckRelocateSpace`)
- [x] Work order: `index_smallest_first` when claiming convert/index jobs (`SortArchivesForIndex`)
- [x] Rescan ops: normal vs `--assume-stable` (scanner + control `rescan` / `serve` tick)
- [x] Rescan resets mount attempts / retryable for present archives (`ResetAllPresentAttempts`)
- [x] Surface `last_scan_at` + scan summary for status/API/SSE (status payload; SSE later)

### Tests

- [x] Stable-file modes
- [x] Rename = new archive_id (scanner rename path + purge/rediscover; store `TestPurgeFreesPath`)
- [x] Partial-download not stable until two identical scans
- [x] Rescan assume-stable bypass
- [x] Content-change fingerprint + content hash same-size change

---

## Phase 4 — Mounter & backends

- [x] Backend selection: `python` (ratarmount) | `rust` (ratarmount-rs) + aliases (`ratarmount`, `ratarmount-rs`)
- [x] Resolve `ratarmount_bin` (config → defaults → PATH → sibling build paths; switch-backend default swap)
- [x] Build CLI:
  - [x] `-f`, `--index-file`, `--write-overlay`, `--recursive` / recursive extensions
  - [x] `-o allow_other` when `windows_visible`
  - [x] index workers (`-P` / equivalent), `extra_ratarmount_args`
  - [x] debug / log: `ratarmount_debug`, `ratarmount_7z_debug`, `ratarmount_log_dir`, `ratarmount_rust_log` (RUST_LOG)
- [x] One child process per mount; process group (`Setpgid` / session)
- [x] Supervise until `ismount` or exit; long index without ismount while `indexing` (`Engine.ProgressLive`)
- [x] Live mount registry: phase (`index_only` | `mount`), `is_first_index`, pid
- [x] Status: first mount `indexing`; remount `mounting` (Engine claim); `progress_label` / `elapsed_s` via status package
- [x] Partial-index cleanup rules (never successfully mounted → delete index)
- [x] Concurrent limits: `max_concurrent_index`, `max_concurrent_mount` (0 = unlimited)
- [x] Timeouts: `mount_ready_timeout_seconds`, `unmount_timeout_seconds` (helpers; serve applies)
- [x] Backoff / `max_mount_attempts` / `mount_retryable` (attempt helpers; no exponential sleep upstream)
- [x] Unmount sequence: SIGTERM group → wait → fusermount → lazy
- [x] Windows visibility / parent dir traverse notes (Linux `o+x` parents) — `docs/architecture.md` + `mounter` package doc; packaging `create-user.sh` o+x; no runtime chmod of arbitrary parents
- [x] Refuse indexes under DrvFs unless `allow_indexes_on_drvfs`
- [x] Nested mount failure parsing / skip summary (line parse; stderr drain deferred to serve)
- [x] `mount` / `unmount` / `unmount --all` / `retry` ops (via `Service.HandleRequest`; CLI socket clients later)
- [x] Note: `recursive_mount` applies at **index build** only; config change needs re-index

### Tests

- [x] Command construction per backend/options
- [x] Partial-index cleanup matrix
- [x] Process-alive / ismount reconcile rules (table-driven)
- [x] Integration: real FUSE when `/dev/fuse` + engine on PATH (`//go:build fuse`; see `docs/dev.md`)

---

## Phase 5 — Convert pipeline (feature parity)

Three convert paths exist upstream; all needed for parity.
**Library + runners** in `internal/convert/` (predicates, bin resolve, cmd construction,
metadata, free-space, limits, `RunZipRepack` / `RunFlattenConvert`). Engine
`PollConvert` / `runConvert` wires archiveconverter → zip → flatten.

### 5.1 External archiveconverter (preferred for solid 7z)

- [x] `archiveconverter_enabled` + bin resolve (config → PATH → sibling release)
- [x] Output dir, mode (`convert` | `convert-single`), backend (`native` | `cli`)
- [x] Level, threads, verify, required, temp_dir, timeout (cmd + timeout helper; required is config-only)
- [x] Native knobs: pipeline, codec, large_threshold, nested_concurrency, nested_size_budget
- [x] basename_match, exclude_inner/outer, rename, extra_args, overhead_bytes
- [x] Convert **before first index**; remount with index skips convert (`ShouldConvert` + `needsIndex`)

### 5.2 Built-in 7z nonsolid / flatten (`convert_7z_*`)

- [x] `convert_7z_nonsolid` + scope (`nested` | `outer` | `flatten` | `all`) helpers
- [x] `convert_7z_bin` (7z), cache dir, overhead, flatten extract buffer (`FlattenParamsFromConfig`)
- [x] `convert_7z_inner_prefix_strip`, `convert_7z_flatten_exclude`
- [x] Parity with Python `sevenzip_nonsolid` helpers (env, cache path, `ShouldFlattenConvert` with injectable probe)
- [x] Flatten process runner (`RunFlattenConvert`): best-effort CLI extract → expand nested `*.7z` → `7z a -ms=off` in-place; injectable `Run7z` / `FlattenNeededFunc`
- [x] Best-effort solid/nested probe via `7z l -slt` (`Parse7zListNeedsFlatten` / `CLIFlattenNeeded` / `DefaultFlattenNeeded`); service wires default when nonsolid+flatten; conservative false on uncertainty
- [x] Nested mini 7z fixture + `*.l-slt.txt` listings under `testdata/nested7z/`; multi/solid generated in TempDir when `7z` on PATH (skip otherwise)
- [ ] **Residual:** no full ratarmountcore solid/folder parser; no stream-flatten path; no encrypted-folder detect; outer nonsolid cache populate still path-only; real FUSE CI deferred; full engine convert still needs `7z` on PATH

### 5.3 ZIP → non-solid 7z repack

- [x] `convert_zip_to_7z` when embedded archives present (`ShouldRepackZip` / zip member suffix scan)
- [x] Zip repack argv (`BuildZipExtractCmd` / `BuildZipCreate7zCmd`) + peak-disk estimate
- [x] Zip repack process runner (`RunZipRepack` / `RepackZipTo7zInplace`) + free-space gate + metadata sidecar; injectable `Run7z`
- [x] Engine wiring (`runZipRepack` via convert job)

### 5.4 Shared convert plumbing

- [x] Status enter/leave `converting` predicates; progress_label `converting to non-solid`
- [x] Convert metadata sidecar read/write (`original_size_bytes`, duration, method, size_delta)
- [x] Concurrent convert limit helper (`LimitReached` / `SlotsAvailable`; 0 = unlimited)
- [x] Disk free-space gates for convert/repack/flatten (`min_free_bytes` + overhead; injectable free probe)
- [x] Doctor reports archiveconverter / 7z backend availability (`internal/doctor`)
- [x] Serve: claim convert → run job → leave converting; wire archiveconverter → zip → flatten (Python `_run_convert` order)

### Tests

- [x] should_convert / should_repack predicates
- [x] Convert cmd construction (archiveconverter + 7z zip extract/create)
- [x] Metadata roundtrip + free-space gate with injectable free-bytes
- [x] Zip repack success/fail with fake 7z scripts + injectable `Run7z`
- [x] Flatten runner success/skip/fail + Engine convert paths (no FUSE)
- [x] Fixtures: nested mini 7z + listings in `testdata/nested7z/` (from upstream mini); multi generated in-test when `7z` present; probe/parse tests skip without 7z

---

## Phase 6 — Reconcile, cleaner, hooks, metrics

### 6.1 Reconcile

- [x] Interval loop + on poll (`service.Tick` → `doReconcile` / `Reconciler.Reconcile`; boot via `service` start)
- [x] Status-dependent liveness (`indexing`/`mounting` vs `mounted`/`hooks_running`) — `internal/reconcile`
- [x] Boot: clear stale PIDs; partial-index cleanup; remount plan — `Boot` / `CleanupPartialIndexes`
- [x] Never re-run terminal-success hooks on remount (fail/remount leaves `hooks_status` intact; coordinates with `hooks.ShouldRunHooks`)

### 6.2 Cleaner

- [x] After `cleanup_after` absence: purge job — `internal/cleaner` (`PurgeAbsentPastGrace` / `Run`)
- [x] Overlay policy: `quarantine` (default) | `delete` | `retain` — `HandleOverlay`
- [x] Quarantine TTL + max bytes prune — `PruneQuarantine`
- [x] Admin purge immediate — `PurgeArchive` (control plane still wires confirm/require_yes later)
- [x] Reappear before grace: clear `removed_at`, keep overlay — scanner `Reappear`; cleaner only targets still-`absent` past grace (documented in `doc.go`)

### 6.3 Hooks

- [x] Discover executables in `hooks.d` (ignore samples/disabled) — `internal/hooks`
- [x] Security: root-owned (or `AllowedOwnerUIDs` service user), not group/other-writable; realpath under hooks dir
- [x] Env protocol `MOUNT_WRAPPER_*` only (D3; no `TARMOUNT_*` dual export)
- [x] argv: mount path, archive path
- [x] Exit codes: `0` success, `75` retryable, other hard fail; timeout → retryable
- [x] Sequential default (`hooks_parallel: false`); optional parallel
- [x] `hooks_stop_on_hard_fail`, `hook_timeout_seconds`, `hook_max_retries`
- [x] `hook_rerun_on_failure`, `hooks_cwd` (`mount` | `archive_dir` | `hooks_dir`)
- [x] Aggregate hooks_status transitions; never re-run terminal success on remount
- [x] CLI: `hooks list` / `hooks status ARCHIVE_ID` (socket ops + `internal/cli`)
- [x] Serve integration: call `hooks.Runner` after first FUSE up (`service` tick `RunForArchive`)

### 6.4 Metrics

- [x] Per-archive: archive size, index size, extracted logical size (index `files` table; mount walk fallback) — `internal/metrics` pure + FS/SQLite providers
- [x] Convert: `convert_source_size_bytes`, convert size delta, `convert_duration_seconds` (helpers + meta provider interface; real convert sidecar reader deferred to convert package)
- [x] Formulas:
  - [x] `space_saved_bytes = max(0, extracted − index)`
  - [x] `space_saved_vs_archive_bytes = max(0, extracted − archive − index)`
- [x] Cache TTL + `no_cache` + `prefer_mount` (CollectorConfig / QueryOptions / MetricsCollector)
- [x] Summary aggregates: totals + max convert duration + counts sized/converted
- [x] Surface in status (`include_sizes` merge via injectable MetricsProvider; control `metrics`, API, SPA)

### 6.5 Status payload (SPA fuel)

- [x] Counts by status (incl. `converting`)
- [x] `low_disk`, `last_scan_at`, `pid`, `version`
- [x] Per-archive dict with progress (`elapsed_s`, `progress_label`, `source_fs`, `pid_alive`)
- [x] Compact `indexing_archives` list for in-progress jobs
- [x] Optional `include_sizes` metrics merge; `errors_recent`; human formatter; helpers (`elapsed_seconds`, `should_log_index_progress`)
- [x] Service `StatusPayload` / control `status` op call `status.Build` (live mounts, free bytes, injectable metrics)

### Tests

- [x] Reconcile table-driven scenarios (`internal/reconcile`; fake probes + OpenMemory store)
- [x] Cleaner grace + quarantine (`internal/cleaner` unit tests: grace, overlay policies, admin purge, age/size prune, path safety, reappear interaction)
- [x] Hook exit-code matrix (`TestClassifyExitMatrix` 0/75/other/timeout + runner success/soft/hard/timeout tests)
- [x] Metrics formulas + summary (`internal/metrics` unit tests; fake size/extracted providers)
- [x] Status progress fields for converting/indexing/mounting (`internal/status` unit tests; fake clock/pid/metrics)

---

## Phase 7 — Control plane & CLI

### 7.1 Unix socket server

- [x] Bind path from config; mode `0660`; group-restricted (service user:group)
- [x] Auth: uid 0 or group membership (Linux); Darwin peercred
- [x] Framing: newline-delimited JSON; `"v": 1` (missing v → treat as 1; unknown → `UNSUPPORTED_VERSION`)
- [x] Ops parity (exact names as upstream unless D6 documents renames):
  - [x] `status` (rich payload via `internal/status`; `include_sizes` when Metrics wired)
  - [x] `metrics` (`archive_id?`, `no_cache?`, `prefer_mount?`)
  - [x] `rescan` (`assume_stable?`), `retry`, `mount`, `unmount` (`target` | `all`), `purge` (`yes`)
  - [x] `hooks_status`, `hooks_list`, `reload`, `stop`
  - [x] `config_get`, `config_set` (`config` | `patch`, `apply`)
- [x] Error envelope: `{ok:false, error, code}` (HandleRequest helpers; full code set later)
- [x] Only serve owns mounts + DB writes (single-writer; Engine + Service)
- [x] Pidfile exclusive flock (`service.PidFile`)
- [x] Unix domain socket **server** bind/auth/framing (`internal/control` + service Start/Tick/Shutdown)

### 7.2 CLI

- [x] `serve [--config] [--once] [--allow-unauth]` (no separate `--foreground` yet; process is foreground)
- [x] `status [--json] [--sizes]`
- [x] `metrics [ARCHIVE_ID] [--no-cache] [--prefer-mount]`
- [x] `rescan [--assume-stable]`
- [x] `retry ARCHIVE_ID`
- [x] `doctor [--json] [--fix-systemd]`
- [x] `config show [--local]` / `config set --json|--file [--patch] [--dry-run]`
- [x] Web: **embedded in `serve`** when `web_enabled` (D4); no separate `web` CLI (upstream had sidecar)
- [x] `mount PATH` / `unmount TARGET|--all` / `purge ARCHIVE_ID --yes`
- [x] `hooks list` / `hooks status ARCHIVE_ID` / `version` (version exists)
- [x] Default config path Linux vs macOS (`config.DefaultConfigPathForHost` / `ResolveConfigPath`)
- [x] Socket-backed cmds require running serve; offline: doctor, config show --local, version
- [x] Socket client via `control.Client` (`internal/cli` wraps `control.NewClient`)

### Tests

- [x] HandleRequest smoke: status / rescan / stop / metrics / config_get (+ unknown op)
- [x] Protocol encode/decode + server/client roundtrip with fake handler
- [x] Auth allow/deny (injectable peercred)
- [x] Stale socket cleanup + service control socket roundtrip
- [x] CLI `serve --help` / unknown flags; offline `version` / `help`
- [x] CLI offline doctor/config show; status without socket → exit 4; parse/help table

---

## Phase 8 — Service loop & doctor

- [x] Main loop: scanner + reconcile + cleaner + Engine progress/work/hooks (`service.Run` / `Tick`)
- [x] Control Unix socket wired into serve (`ServeReady` each tick); optional HTTP still deferred (Phase 9)
- [x] Signal handling: SIGTERM/SIGINT stop; SIGHUP reload request
- [x] Shutdown: unmount live/mounted/indexing/mounting; close inotify; release pidfile; close DB
- [x] `TimeoutStopSec` guidance (300s+) — unit + install/architecture docs
- [x] Doctor library (`internal/doctor`): report + injectable probes + text/JSON formatters
- [x] Doctor CLI: `mount-wrapper doctor [--json] [--fix-systemd] [--config PATH]` (+ `GET /api/doctor`)
  - [x] Python N/A; instead Go version + ratarmount bin + backends
  - [x] FUSE device / unmount tool
  - [x] `user_allow_other` when windows_visible
  - [x] source dir readability
  - [x] path layout (indexes not on DrvFs)
  - [x] systemd presence / drop-in generation (`--fix-systemd` via library)
  - [x] service user / macOS login-user messaging
  - [x] archiveconverter availability (+ 7z when convert features enabled)
  - [x] peercred / control socket notes; free space on key paths
- [x] Logging: `log/slog` in service/engine (journald-friendly foreground stderr)

### Tests

- [x] Doctor report JSON shape (`FormatJSON` / `ToMap` + unit tests)
- [x] Drop-in generation contents (`BuildSystemdDropin` / fix-systemd tests)
- [x] Shutdown clears pidfile; pidfile exclusive second acquire fails
- [x] Tick once with temp dirs + injectable Engine StartProcess (no real FUSE)

---

## Phase 9 — HTTP API (for SPA)

- [x] Bind `web_host` / `web_port` (default `127.0.0.1:8787`); warn if non-loopback
- [x] Optional Bearer `web_token` on `/api/*` (+ `?token=` for GET convenience, parity)
- [x] Endpoints (parity with upstream `web.py` + reactive additions):
  - [x] `GET /api/health` — web ok + serve reachable + pid/version
  - [x] `GET /api/status` / `GET /api/status/sizes`
  - [x] `GET /api/archives` — merged status + metrics + summary + counts
  - [x] `GET /api/metrics`
  - [x] `GET /api/config` / `POST /api/config` (`config|patch`, `apply`, dry-run via apply:false)
  - [x] `POST /api/rescan` / `unmount` / `retry` / `purge`
  - [x] `GET /api/doctor` (in-process; no socket required)
  - [x] `GET /api/wsl-info` (UNC hint from `WSL_DISTRO_NAME`)
- [x] **SSE stream** `GET /api/events` (D8 preferred; WebSocket only if needed):
  - [x] Event types: `snapshot` | `counts` | `heartbeat` | `archive` | `scan` | `metrics` | `low_disk`
  - [x] Payload includes progress fields (`elapsed_s`, `progress_label`, status) via status map
  - [x] Initial full snapshot on connect; then delta events on change + full `snapshot` every Nth tick (default 4)
  - [x] Diff helper: counts / archive (status·progress·error·paths) / scan (`last_scan_at`) / low_disk edge / optional metrics_summary
  - [x] Configurable `SSEInterval`, `HeartbeatInterval`, `SSEFullSnapshotEvery`, `SSEIncludeSizes` on Server options
  - [x] Optional `ChangeNotifier` Backend for push wake; production `APIBackend` + service tick/control ops notify (ticker remains fallback)
  - [x] Heartbeat comment/event so proxies keep connection
  - [x] Auth same as `/api/*`
- [x] Serve embedded SPA static assets (`/`, `/settings` → index; Vite `/assets/*`)
- [x] CORS: not required for same-origin embed; Vite proxy documented in `docs/dev.md`
- [x] Prefer **embedded in serve** (D4) so status events don't require a second process

### Tests

- [x] API auth 401
- [x] Action happy paths with fake Backend
- [x] Archives merge shape (metrics + summary)
- [x] SSE snapshot on connect (reconnect UX is SPA Phase 10)
- [x] SSE pure diff helper unit tests + integration deltas (status change → archive/counts/scan/low_disk)
- [x] SSE ChangeNotifier wake test + service NotifyChange coalescing

---

## Phase 10 — TypeScript SPA (reactive upgrade over vanilla upstream UI)

Upstream is vanilla JS + 15s poll. Target is a **reactive** SPA with live updates, same operator surface, better UX.

### 10.1 Shell

- [x] App shell: nav Archives | Settings; theme toggle (persist preference)
- [x] Connection badge: connected / reconnecting / service-down
- [x] API client (fetch + Bearer token; inject token when configured)
- [x] SSE client with exponential backoff reconnect + fallback poll (e.g. 15s) when SSE fails
- [x] Loading / service-down / error banners + action toasts
- [x] Dev: Vite proxy to Go; Prod: embedded assets
- [x] Shared stores/state (Svelte 5 runes module): archives, overview, config, connection

### 10.2 Archives page (parity columns + reactive)

- [x] Overview pills: counts for mounted/converting/indexing/mounting/discovered/hooks/failed/absent + version + **low disk**
- [x] Savings summary bar: space saved, extracted, indexes, archives, original, convert delta, longest convert, sized/converted counts
- [x] Table columns (parity with upstream):
  - [x] Name, Status (+ progress_label), Hooks
  - [x] Archive size, Original size (+ convert delta), Convert time
  - [x] Index size, Index time, Mount time
  - [x] Extracted, Saved (vs extract), Saved (vs archive)
  - [x] Mount / path, Actions
- [x] In-progress rows: `elapsed_s`, row highlight for converting/indexing/mounting; failed highlight
- [x] Filter by status (include `converting`); multi-column sort (+ desc)
- [x] Live updates via SSE (patch row / counts without full page flash); fallback poll
- [x] Row actions: Copy mount path (and/or archive path), Retry, Unmount, Purge
- [x] Global: Rescan, Rescan assume-stable, Unmount all, Doctor, Theme, Auto-refresh toggle
- [x] Doctor panel (inline card with check summary + raw JSON), not only a deep-link
- [x] Windows UNC mount hint when WSL (`/api/wsl-info`)
- [x] Confirm destructive actions (purge, unmount all, overlay_cleanup=delete elsewhere)
- [x] Optional: row expand / raw JSON details (upstream has “Show raw JSON”)
- [x] Optional: per-archive hooks detail (`hooks status`) drawer (`GET /api/hooks?archive_id=`, SPA drawer)

### 10.3 Settings page

- [x] Grouped form for **all public config keys** (mirror upstream `SETTINGS_SCHEMA` groups):
  - [x] Sources, Paths, Discovery, Mount/ratarmount, Hooks, Cleanup, Web, Logging
  - [x] Include full convert + archiveconverter field set (see Appendix D)
- [x] Validate (dry-run) + Apply; Reload from service
- [x] Banners: hot-reload vs restart-required (consume API classification lists)
- [x] Confirmations for destructive options (empty `source_dirs`, `overlay_cleanup=delete`)
- [x] Mark restart-required fields in UI
- [~] Type-safe form models — hand-written TS types (`web/src/lib/api-types.ts` + `types.ts`, D11); OpenAPI / shared JSON schema later

### 10.4 Reactive UX polish

- [x] Optimistic or pending state on row actions (disable double-submit)
- [x] Accessible controls; keyboard basics
- [x] Responsive enough for laptop operator use
- [x] Empty states + doctor from errors / low disk
- [x] Formula help text (space saved vs extract / vs archive / original)

### Tests

- [x] Component/unit tests for formatters (bytes, durations, status labels, convert delta)
- [x] Store/SSE client unit tests (reconnect, snapshot apply) — backoff + table filter/sort unit tests
- [x] Playwright smoke optional — `web/e2e` mocks `/api/*` (no daemon); `RUN_E2E=1 npm run test:e2e` / `make web-e2e`; not in default CI (optional `workflow_dispatch` job). Residual: settings-page validate E2E

---

## Phase 11 — Packaging & install

### 11.1 Linux (Ubuntu)

- [x] systemd unit: `User=mount-wrapper` `Group=mount-wrapper` (D9 decided), RuntimeDirectory, DeviceAllow fuse, hardening baseline (`TimeoutStopSec=300`, `ProtectSystem=strict`, `EnvironmentFile=-/etc/mount-wrapper/env`)
- [x] Optional web: embedded only (D4); **no** sidecar web unit
- [~] `.deb` packaging (dh or nfpm/goreleaser) — `packaging/nfpm.yaml` + goreleaser nfpms sketch; **not** CI publish
- [~] postinst: user/group, dirs, `user_allow_other` when possible — `packaging/scripts/create-user.sh` (operators); nfpm postinstall hook residual
- [x] ship example config, hooks sample, wsl.conf snippet (`packaging/examples/*`, `packaging/wsl.conf.snippet`); man page residual
- [x] ship `windows-task-scheduler.xml.example` (WSL boot without relying only on docs)
- [x] Document engine install (ratarmount / ratarmount-rs / archiveconverter / fuse3); optional Recommends in nfpm sketch
- [x] **Do not** require vendoring Python ratarmount in Go package

### 11.2 Rocky 8

- [~] Build against glibc 2.28 **or** musl static binary — document `CGO_ENABLED=0` pure-Go interim; musl matrix residual (D7)
- [~] `.rpm` via nfpm/goreleaser — sketch only
- [x] systemd unit parity (same unit file)
- [x] Document fuse3 / ratarmount install on Rocky (`docs/install.md`)
- [ ] CI job or container smoke on rocky:8

### 11.3 macOS

- [x] launchd user agent plist example
- [x] Homebrew formula sketch (deps: macfuse notes; engines external)
- [x] Document macFUSE permission prompts (`docs/macos.md`, `docs/install.md`)
- [x] Socket path length limits under Caches
- [x] Friend-test checklist (port from upstream `docs/macos.md`)

### 11.4 Release

- [x] goreleaser sketch: linux amd64/arm64, darwin amd64/arm64 (`.goreleaser.yaml`; not CI-wired)
- [x] SHA256SUMS + release notes template (goreleaser checksum + release header)
- [x] Version command + build ldflags (Makefile already; documented in install/dev)

---

## Phase 12 — Parity verification & cutover

- [x] Feature checklist vs tarmount-wsl README table (all rows) — `tools/parity/feature_checklist.md`
- [x] CLI surface diff (commands/flags) — scripted inventory `tools/parity/cli_surface.sh` → `cli_surface.md`
- [x] Config key inventory 1:1 (Appendix D) or documented renames/aliases — `gen_config_keys.sh` + `config.PublicKeys`
- [x] Socket op inventory 1:1 (`status`, `metrics`, `config_get/set`, …) — `tools/parity/socket_ops.sh`
- [x] Status/API JSON shape diff (archives table columns + convert metrics) — summarized in `docs/parity.md`
- [x] SPA surface checklist vs upstream web UI (Appendix E) — residual: OpenAPI-generated client only (hand-written types + Playwright smoke landed)
- [x] Socket protocol compatibility layer? (**decide:** **new protocol only** — D6; no dual; documented in migration/parity/socket_ops)
- [x] WSL manual: DrvFs source, UNC visibility, boot remount, Task Scheduler note — `docs/parity.md` checklist
- [x] Rocky 8 manual: install rpm, mount tar.gz/zip, doctor — `docs/parity.md` checklist
- [x] macOS manual: macFUSE mount/unmount/service — `docs/parity.md` + `docs/macos.md`
- [x] Performance: large archive index still bounded by engine; orchestrator overhead negligible (documented residual)
- [x] Migration guide: Python → Go (state.db same schema new path; no auto-open old DB — D5) — `docs/migration.md`
- [x] Deprecation plan for tarmount-wsl (soft replace D13) — `docs/migration.md`

---

## Phase 13 — Hardening & polish

- [x] Structured logging fields (`archive_id`, `event=…`) — key service/engine/relocate paths use slog attrs
- [x] Metrics endpoints optional Prometheus later — `GET /metrics` Prometheus text (hand-written; no client_golang); open on loopback bind, token on non-loopback
- [x] Rate-limit destructive API actions — per-IP min-interval on POST purge / unmount-all / rescan (429 `RATE_LIMITED`)
- [x] Fuzz config/YAML and socket decoder — `FuzzParseRequest`, `FuzzLoadText`
- [x] Security review: hooks path escape, token default empty, loopback bind — `docs/security.md`
- [x] Man page + docs site (or keep markdown in `docs/`) — `packaging/man/mount-wrapper.1` (markdown docs remain primary)
- [x] Remove debug escapes; audit `TARMOUNT_CONTROL_ALLOW_UNAUTH` equivalent — only `MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH`; no dual `TARMOUNT_*` hook export

---

## Decisions log

| ID | Decision | Choice | Status |
|----|----------|--------|--------|
| D1 | SPA framework | **Svelte 5** | decided |
| D2 | CLI / package binary name | **`mount-wrapper` only** (no `tarmount-wsl` alias) | decided |
| D3 | Hook env prefix | **`MOUNT_WRAPPER_*` only** (no `TARMOUNT_*`, no dual export) | decided |
| D4 | Web process model | **Embedded in `serve`** (`web_enabled` / flag) | decided |
| D5 | State DB schema strategy | **Same logical schema as Python (001–006+)** at new path; no auto-open of old `tarmount-wsl` DB | decided |
| D6 | Socket protocol strategy | **New protocol only**; familiar op names/shapes where practical; no dual-daemon guarantee | decided |
| D7 | Rocky 8 binary linking | **musl static** for release matrix | decided |
| D8 | Reactive transport | **SSE** + ~15s poll fallback | decided |
| D9 | Service user/group | **`mount-wrapper` / `mount-wrapper`** | decided |
| D10 | FHS / config path prefix | **`/etc`, `/var/lib`, `/run` + `mount-wrapper`** | decided |
| D11 | OpenAPI / typed SPA client | **Hand-written TS types** for now | decided |
| D12 | Go module path | **`github.com/hilather/mount-wrapper`** | decided |
| D13 | Migration stance from tarmount-wsl | **Soft replace** — document migration; no dual install tooling | decided |
| D14 | Default mount backend | **`rust` (ratarmount-rs)** | decided |
| D15 | Branding in UI | **`mount-wrapper` everywhere** + README credit to tarmount-wsl | decided |

### Locked implications (apply in Phase 0+)

**Identity & packaging**
- Binary / package / unit: `mount-wrapper`, `mount-wrapper.service`
- User/group: `User=mount-wrapper` `Group=mount-wrapper`
- Paths: `/etc/mount-wrapper/config.yaml`, `/var/lib/mount-wrapper/…`, `/run/mount-wrapper/control.sock` + pidfile
- Socket: `mount-wrapper:mount-wrapper` mode `0660`; auth = root **or** group `mount-wrapper`
- License: MIT
- Go module / remote: `github.com/hilather/mount-wrapper`

**Runtime defaults**
- Mount backend default: `rust` / ratarmount-rs resolution chain
- Web: embedded in serve; bind `127.0.0.1:8787`; optional `web_token`
- Live UI: SSE (`GET /api/events`) + poll fallback
- Poll 60s; overlay cleanup `quarantine`
- Hooks env: `MOUNT_WRAPPER_*` only (argv still mount path, archive path)
- No dual `TARMOUNT_*` export (no production hooks to preserve)

**Compatibility stance**
- Feature parity with tarmount-wsl behavior; clean names/paths/protocol (not a drop-in binary)
- Schema shape matches Python migrations for easier mental model / optional later import; **default DB path is new**
- Soft replace: migration guide in Phase 12; no requirement to co-install with Python package

**Frontend**
- Svelte 5 + Vite + TypeScript under `web/`
- Hand-written API types; OpenAPI optional later
- Theme: system + toggle, localStorage

**Release / CI**
- goreleaser: linux/darwin amd64+arm64; prefer musl static for Linux where practical
- CI: Ubuntu unit tests first; Rocky/macOS later

### D12 note
Module path and git remote: `github.com/hilather/mount-wrapper`.

---

## Appendix A — Module map (Python → Go)

| tarmount-wsl | Go package (proposed) |
|--------------|------------------------|
| `cli` | `cmd/mount-wrapper`, `internal/cli` |
| `config`, `config_io` | `internal/config` |
| `platform_support` | `internal/platform` |
| `paths` | `internal/paths` |
| `state` + `migrations` | `internal/state` |
| `scanner` | `internal/scanner` |
| `mounter`, `backends` | `internal/mounter` |
| `converter`, `sevenzip_*`, `zip_repack` | `internal/convert` |
| `archives` (relocate) | `internal/archives` |
| `reconcile` | `internal/reconcile` |
| `cleaner` | `internal/cleaner` |
| `hooks` | `internal/hooks` |
| `control` | `internal/control` |
| `service` | `internal/service` |
| `doctor` | `internal/doctor` |
| `metrics`, `status` | `internal/metrics`, `internal/status` |
| `web` + `web_static` | `internal/api` + `web/` (TS) |
| `match` | `internal/match` |

---

## Appendix B — Suggested implementation order (critical path)

1. Phase 0 bootstrap  
2. Config + platform + paths (full key inventory)  
3. State + scanner (discover only; include `converting` in schema early)  
4. Mounter + serve loop (mount tar/zip)  
5. Control socket + CLI status/unmount/metrics  
6. Reconcile + cleaner + hooks  
7. Convert pipeline (archiveconverter + built-in 7z + zip repack)  
8. Doctor + packaging Linux  
9. HTTP API (parity endpoints) + status progress fields  
10. SPA archives + SSE reactive core  
11. SPA settings (full schema)  
12. Rocky + macOS packaging  
13. Full parity verification (CLI / config / API / SPA checklists)  

---

## Appendix C — Out of scope (unless reopened)

- [ ] Reimplementing ratarmount inside Go (keep external engines)
- [ ] Windows native service / non-WSL FUSE
- [ ] WSL1
- [ ] Explorer shell extension
- [ ] Auto-commit write overlays into archives
- [ ] Full FSEvents watcher (optional later on macOS)
- [ ] Multi-user remote IAM for web UI (loopback + optional token only)
- [ ] Vendored private Python ratarmount venv inside the Go package

---

## Appendix D — Config key inventory (parity checklist)

Mirror upstream `Config` + public snapshot. Mark each key implemented in Go + exposed in SPA settings.

### Sources & paths
- [x] `source_dirs`, `recursive`, `name_regex`
- [x] `mount_root`, `index_dir`, `overlay_dir`, `state_db`, `hooks_dir`
- [x] `archives_dir`, `move_archives_to_linux`, `archive_relocate_overhead_bytes`
- [x] `control_socket`, `pid_file`

### Discovery
- [x] `poll_interval_seconds`, `reconcile_interval_seconds`, `use_inotify`
- [x] `stable_file_mode`, `min_file_age_seconds`
- [x] `content_fingerprint`, `on_content_change`, `max_archive_bytes`

### Mount / ratarmount
- [x] `recursive_mount`, `recursive_mount_extensions`, `index_smallest_first`
- [x] `write_overlay`, `windows_visible`, `allow_indexes_on_drvfs`
- [x] `mount_backend`, `ratarmount_bin`, `ratarmount_index_workers`
- [x] `ratarmount_debug`, `ratarmount_7z_debug`, `ratarmount_log_dir`, `ratarmount_rust_log`
- [x] `extra_ratarmount_args`
- [x] `max_concurrent_index`, `max_concurrent_convert`, `max_concurrent_mount`
- [x] `max_mount_attempts`, `mount_ready_timeout_seconds`, `unmount_timeout_seconds`

### Built-in 7z convert
- [x] `convert_7z_nonsolid`, `convert_7z_scope`, `convert_7z_bin`, `convert_7z_cache_dir`
- [x] `convert_7z_overhead_bytes`, `convert_7z_flatten_extract_buffer_bytes`
- [x] `convert_7z_inner_prefix_strip`, `convert_7z_flatten_exclude`
- [x] `convert_zip_to_7z`

### archiveconverter
- [x] `archiveconverter_enabled`, `archiveconverter_bin`, `archiveconverter_output_dir`
- [x] `archiveconverter_mode`, `archiveconverter_backend`, `archiveconverter_level`
- [x] `archiveconverter_threads`, `archiveconverter_verify`, `archiveconverter_required`
- [x] `archiveconverter_temp_dir`, `archiveconverter_native_pipeline`, `archiveconverter_native_codec`
- [x] `archiveconverter_native_large_threshold`, `archiveconverter_nested_concurrency`
- [x] `archiveconverter_nested_size_budget`, `archiveconverter_basename_match`
- [x] `archiveconverter_exclude_inner`, `archiveconverter_exclude_outer`, `archiveconverter_rename`
- [x] `archiveconverter_extra_args`, `archiveconverter_overhead_bytes`, `archiveconverter_timeout_seconds`

### Hooks / cleanup / web / logging
- [x] `hooks_parallel`, `hooks_stop_on_hard_fail`, `hook_timeout_seconds`
- [x] `hook_max_retries`, `hook_rerun_on_failure`, `hooks_cwd`
- [x] `cleanup_after`, `overlay_cleanup`, `quarantine_retain_for`, `quarantine_max_bytes`, `min_free_bytes`
- [x] `web_enabled`, `web_host`, `web_port`, `web_token`
- [x] `log_level`, `strict_config`, `version`

---

## Appendix E — SPA surface checklist (vs upstream web UI)

### Archives
- [x] Overview status pills (incl. converting, low_disk)
- [x] Savings summary (incl. convert totals)
- [x] Full metrics table columns (Appendix D metrics / durations)
- [x] Filter + sort (name, status, sizes, durations)
- [x] Progress labels + elapsed for in-progress
- [x] Row actions: copy, retry, unmount, purge (+ Hooks detail)
- [x] Global: rescan, rescan assume-stable, unmount all, doctor, theme
- [x] WSL UNC hint
- [x] Action toasts / errors when service down
- [x] **Reactive:** SSE live updates (upgrade over 15s poll)

### Settings
- [x] All groups/fields from upstream `SETTINGS_SCHEMA`
- [x] Validate + apply + reload
- [x] Hot vs restart banners; destructive confirms

### Reactive upgrades (beyond parity)
- [x] SSE connection lifecycle UI
- [x] Store-driven re-render without full table wipe
- [x] Pending/disabled actions during requests
- [x] Optional hooks detail drawer (`GET /api/hooks?archive_id=`, focus trap / Escape)
- [~] Optional typed API client — **done as hand-written** `web/src/lib/api-types.ts` re-export + `types.ts` / `api.ts` (D11); **not** OpenAPI codegen (residual)
- [x] Optional Playwright E2E smoke — mocked API shell (`Archives` heading + connection badge); vitest still covers format/SSE/hooks helpers; settings validate E2E residual

---

## Gap review notes (2026-07-31)

Compared local `tarmount-wsl` (Python + vanilla web) to this TODO. **Was already solid** on architecture, phases, packaging platforms, and module map. **Filled gaps:**

1. `converting` is first-class (not optional) + full transition table  
2. Dual convert stacks: archiveconverter **and** built-in `convert_7z_*` **and** zip repack  
3. Status progress fields (`elapsed_s`, `progress_label`, `indexing_archives`, `low_disk`)  
4. Convert metrics columns/summary (original size, duration, delta)  
5. Complete config key inventory (Appendix D)  
6. Exact control ops (`config_get`/`config_set`, metrics flags)  
7. SPA table/overview parity + SSE event design (Appendix E)  
8. Packaging helpers: Task Scheduler example; service user naming  
9. Decisions D9–D11  

---

*Generated from tarmount-wsl evaluation for Go + TypeScript SPA rewrite. Updated after parity review against local upstream. Update checkboxes as work lands.*
