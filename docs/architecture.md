# Architecture (target)

See [TODO.md](../TODO.md) for the full phased plan and decisions log.

```text
SPA (TS/Svelte) ──HTTP/SSE──► Go serve (SQLite, scanner, mounter, hooks, cleaner)
CLI ────────────UDS JSON────► same process
                                │ exec
                          ratarmount-rs / archiveconverter / 7z tools
```

| Layer | Choice (decided) |
|-------|------------------|
| Module | `github.com/hilather/mount-wrapper` |
| Binary / service user | `mount-wrapper` |
| Paths | `/etc`, `/var/lib`, `/run` + `/mount-wrapper` |
| Web | Embedded in `serve`; SSE + poll fallback |
| Hooks env | `MOUNT_WRAPPER_*` |
| Default engine | ratarmount-rs (`rust`) |

## Package map

Python `tarmount-wsl` → Go packages: see TODO.md Appendix A.

SPA source lives in `web/`; production assets are copied into `internal/webui/dist` and embedded with `embed.FS`.

## Foundations (implemented)

### Phase 1 — config & platform

| Package | Responsibility |
|---------|----------------|
| `internal/config` | YAML schema v1 load/validate, duration parse/format, public snapshot, hot/restart key sets, patch merge, atomic write |
| `internal/platform` | Host/WSL detection, Linux/macOS path profiles, FUSE probes, unmount argv, peer credentials, `MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH` |
| `internal/paths` | Drive-letter → `/mnt/<letter>`, UNC reject, DrvFs detect, mount-name sanitize, service directory creation |

### Phase 2 — SQLite state

| Package | Responsibility |
|---------|----------------|
| `internal/state` | SQLite store (WAL + FKs), migrations 001–006 (schema v6), archive lifecycle statuses + `ALLOWED_TRANSITIONS`, optimistic claims, hooks rows, purge CASCADE, meta, content-change reset/keep hooks |

**Single-writer:** only the `serve` process should open a writing `Store` in production. `Store` uses `MaxOpenConns(1)`.

**Statuses:** `discovered` → `converting` / `indexing` / `mounting` … → `mounted` / `hooks_running` → `unmounting` → `absent`. Purge is row `DELETE` (no durable `purged` status).

### Phase 3 — scanner (library)

| Package | Responsibility |
|---------|----------------|
| `internal/match` | `name_regex` compile, extension allow-list, basename filter |
| `internal/scanner` | Source walk, stable-file gate, fingerprint, store reconcile, `Scan(assumeStable)`, Linux inotify hint (poll remains authoritative; never watch DrvFs) |
| `internal/archives` | Path classification under `archives_dir` / converter output; `ShouldRelocate` / `RelocateArchive` / free-space gate / sidecar move; convert metadata path via `internal/convert.MetadataPath` |
| `internal/convert` | Convert pipeline library (see Phase 5) |

**Wired:** `service.Tick` poll loop, control `rescan`, Engine sync relocate, status `last_scan_at`, HTTP SSE snapshot + deltas (Phase 9).

### Phase 4 — mounter core (library + Engine)

| Package | Responsibility |
|---------|----------------|
| `internal/mounter` | Backend normalize/resolve, ratarmount argv + child env, live registry (`index_only`/`mount`), process-group start/kill/wait, unmount sequence (SIGTERM → fusermount → lazy), partial-index cleanup, concurrent-limit + mount-attempt helpers, DrvFs index refuse, nested-automount stderr drain + skip summary |
| `internal/mounter.Engine` | Claim + spawn, `CheckChild` / index→mount, mark mounted/failed, convert jobs (async: archiveconverter → zip repack → flatten), relocate (sync v1), `ProgressLive`, `Unmount` |

**Nested automount skips:** while a ratarmount-rs child runs, Engine pipes stderr and parses lines matching `Mounting of '…' failed because of: …`. Paths accumulate on `ManagedMount.SkippedNested`. On failure, `MarkFailed` enriches `last_error` with `skipped N nested mounts: path…` (sample paths). On success / index→mount, a summary is logged (`event=index_nested_skipped`); `last_error` is cleared when mounted.

**Still deferred:** stream-flatten / full solid-folder parse. Flatten + outer-cache probes are best-effort CLI (`7z l -slt`), not ratarmountcore. Optional real-FUSE smoke: `go test -tags=fuse ./internal/mounter/` (skips without `/dev/fuse` or engine on PATH; not in default `make test`).

#### Windows visibility / parent traverse (Linux + WSL)

When `windows_visible: true`, FUSE mounts use `-o allow_other` so Windows UNC
(`\\wsl.localhost\<distro>\…`) and other local users can open the mount. That
requires:

1. **`user_allow_other`** in `/etc/fuse.conf` (`create-user.sh` enables when possible; doctor warns when missing).
2. **Other-executable parents** on the path to each mountpoint: every directory
   from `/` down through `mount_root` (and typically `…/mounts`) needs `o+x`
   (world traverse). FUSE content is still gated by the mount; only path
   resolution needs `x` on parents.
3. **Packaging default:** `create-user.sh` runs
   `chmod o+x /var/lib/mount-wrapper /var/lib/mount-wrapper/mounts` best-effort.
4. **Custom roots:** if `mount_root` is under a home or other non-world-x tree,
   the operator must chmod parents; the daemon does **not** auto-chmod arbitrary
   parents (would widen multi-user WSL exposure).

macOS: keep `windows_visible: false` (single-user agent; allow_other not the WSL path).

### Phase 5 — convert pipeline (library + runners)

| Package | Responsibility |
|---------|----------------|
| `internal/convert` | Sidecar `*.tarmount-convert.json` read/write; archiveconverter bin resolve + argv; `ShouldConvert`; 7z nonsolid scope/env/cache + `ShouldFlattenConvert` (injectable probe); zip→7z repack predicates/cmd/peak-disk; **process runners** `RunZipRepack` / `RunFlattenConvert` / `EnsureNonsolidCachedCopy` (injectable `Run7zFunc` / `List7zFunc`); free-space gates; concurrent convert limit; progress_label / enter-leave converting |

**Paths:**

| Path | Role |
|------|------|
| archiveconverter | Preferred solid→non-solid for `.7z` when enabled; output under `archiveconverter_output_dir/{archive_id}.7z` |
| 7z nonsolid | `convert_7z_scope`: nested/outer/all apply child env; flatten is pre-mount in-place via `RunFlattenConvert` when `FlattenNeededFunc` says true |
| zip repack | When `convert_zip_to_7z` + nonsolid and zip has embedded archive members → `RunZipRepack` → stored non-solid `.7z` beside source (+ metadata sidecar) |
| Flatten probe | Default when nonsolid + scope `flatten`: `convert.DefaultFlattenNeeded` → `7z l -slt` heuristics (`Solid=+` or nested member `*.7z`); **false** on uncertainty / missing 7z / encryption markers (`Encrypted=+`, Wrong password, …) |
| Outer nonsolid cache | Scope `outer`/`all`: mount path calls `EnsureNonsolidCachedCopy` — solid → CLI extract + `a -ms=off` under content-keyed cache dest; non-solid keeps source; encrypted fails with clear error |

**Engine convert order** (parity with Python `_run_convert`): archiveconverter (if available / `.7z` / not zip-repack) → zip repack → flatten. Success updates `archive_path` + fingerprint, leaves `converting` → `discovered`, then continues to mount/index. Failure → `index_failed` / `mount_failed` with `last_error`. Outer/all cache populate runs at **mount** start (not the convert job), like Python `resolve_mount_archive_path`.

**Residual gaps:**

| Gap | Detail |
|-----|--------|
| Solid/nested probe | Best-effort `7z l -slt` only — not ratarmountcore solid-folder parse; encrypted detect is CLI phrase/`Encrypted=+` only; inject `NeedsFlatten` to override |
| Flatten depth | Best-effort CLI: extract, walk nested `*.7z`, repack `-ms=off`; encrypted refused clearly; **no stream-flatten** / post-rebuild nested-header check |
| Outer cache | CLI extract+repack only (no stream-repack / flock); nested `.7z` members not expanded in outer cache (child env still used for nested when scope allows) |
| Real engines in CI | Unit tests use fake 7z scripts / injectable `Run7z` / list output; nested mini + encrypted `*.l-slt.txt` under `testdata/nested7z/` for offline parse; real `7z l` / multi generation skips when 7z missing; no FUSE required for default `make test` |

### Phase 6.1 — reconcile (library)

| Package | Responsibility |
|---------|----------------|
| `internal/reconcile` | Status-aware PID/ismount liveness (`indexing`/`mounting`/`converting` vs `mounted`/`hooks_running`), pure `Action` decisions, apply (fail/absent/partial-index), boot remount plan (clear stale PIDs, requeue, `request_remount` via `mount_failed`), injectable probes |

**Rules:**

- In-progress (`indexing` / `mounting`): never fail on “not ismount” alone; fail on dead PID, supervised exit, or `mount_ready` timeout.
- Healthy mount (`mounted` / `hooks_running`): require **both** ismount and live PID; archive missing → `absent`, else → `mount_failed`.
- Fail/remount paths **do not** reset `hooks_status` (terminal success is never re-run; see `hooks.ShouldRunHooks`).
- Boot: mid-flight work requeued; previously mounted rows go to `mount_failed` with `mount_retryable` and cleared PID.

**Serve call site (wired in `internal/service`):**

```text
// Start:
reconciler.CleanupPartialIndexes()
reconciler.Boot()
// Tick:
//   scan (poll_interval) → reconcile (reconcile_interval) → cleaner
//   Engine.ProgressLive / PollConvert / PollRelocate
//   startPendingWork / runPendingHooks
```

### Phase 6.2 — cleaner (library)

| Package | Responsibility |
|---------|----------------|
| `internal/cleaner` | Grace purge of `absent` rows past `cleanup_after` (`ListAbsentPastGrace` + `PurgeArchive`), overlay policy (`quarantine` / `delete` / `retain`), quarantine age + max-bytes prune, admin immediate purge, stale mount-dir cleanup under `mount_root`, optional ratarmount `/tmp/.tmp*` prune, `min_free_bytes` disk check |

**Path safety:** index/overlay/mount deletes and quarantine moves are refused unless the path resolves under `index_dir`, `overlay_dir`, or `mount_root` respectively. Paths outside roots are left on disk; DB row may still be purged so rediscovery is not blocked.

**Reappear interaction:** scanner `Reappear` clears `removed_at` and keeps overlay/index paths. Cleaner grace purge only sees still-`absent` rows with `removed_at` past grace — it does not re-mark absent or fight the scanner. Admin `PurgeArchive` is explicit.

**Wired:** serve cleanup cadence + control `purge` + Engine as `Unmounter` / live paths.

### Phase 6.3 — hooks (library)

| Package | Responsibility |
|---------|----------------|
| `internal/hooks` | Discover executables in `hooks.d` (ignore `*.sample` / `*.disabled` / README*), security (owner + not g+w/o+w + realpath under hooks dir), `MOUNT_WRAPPER_*` env protocol (no `TARMOUNT_*`), argv mount/archive paths, exit 0/75/timeout classification, sequential or `hooks_parallel`, stop-on-hard-fail, retries, aggregate `hooks_status`, skip terminal success on remount |

**Wired:** `service.Tick` → `runPendingHooks` after mount work; first-mount `RunForArchive` when mounted + `ShouldRunHooksRecord`; control `hooks_list` / `hooks_status` (+ CLI socket clients). Exit-code matrix (0 / 75 / other / timeout) covered by `internal/hooks` unit tests.

### Phase 6.4 — metrics (library)

| Package | Responsibility |
|---------|----------------|
| `internal/metrics` | Space-saved formulas, per-archive size fields, convert delta/duration helpers, summary aggregates, TTL cache, `MetricsCollector` with injectable `SizeProvider` / `ExtractedSizeProvider` / `ArchiveSource` |

**Implemented:** pure formulas + FS/SQLite index sum + mount walk fallback; unit tests with map fakes and minimal synthetic indexes.

**Wired:** control `metrics` op via `MetricsCollector` + store `ArchiveSource` adapter in service. CLI/API/SPA surfaces still pending; convert-metadata sidecar reader wiring (`ConvertMetaProvider` ready — use `convert.ReadConvertMetadata` adapter).

### Phase 6.5 — status payload (SPA fuel)

| Package | Responsibility |
|---------|----------------|
| `internal/status` | Rich status JSON for control/SPA: counts by lifecycle status (incl. `converting`), top-level convenience counts, per-archive dicts with progress (`elapsed_s`, `progress_label`, `source_fs`, `pid_alive`, optional `is_mounted`), compact `indexing_archives`, `errors_recent`, disk free / `low_disk`, `last_scan_at` / scan summary, optional `include_sizes` metrics merge, human formatter, helpers (`ElapsedSeconds`, `ShouldLogIndexProgress`) |

**Build inputs (injectable):** archives, live mounts (`index_only` / `mount` phases), clock/`now`, `PIDAlive`, `FreeBytes`, `IsMount`, `MetricsProvider`.

**Wired:** `service.StatusPayload` / `StatusPayloadOpts` → `status.Build`; control op `status` accepts `include_sizes` (metrics merge when `Service.Metrics` is set).

**Progress labels:** `converting to non-solid` · `indexing` / `building index` (live `index_only`) · `mounting` / `mounting FUSE` (live `mount`) · `hooks` (`hooks_running`).

**Wired:** production Metrics collector on serve + control `metrics` / status `include_sizes`. CLI `status` exists (7.2). HTTP `/api/status` + SSE snapshot/deltas (Phase 9). SPA reactive table still Phase 10.

### Phase 7.1 — Unix control plane

| Package | Responsibility |
|---------|----------------|
| `internal/control` | JSON-lines framing (`ParseRequest` / `EncodeResponse`), peercred auth (root or group `mount-wrapper`), `Server` (bind `0660`, stale cleanup, optional chown, `ServeReady`), `Client` (`Request` / `RequestOK`) |
| `internal/service` | Creates `control.Server` when `control_socket` set; `Tick` → `ServeReady`; `Shutdown` closes socket; `HandleRequest` is the handler |

**Protocol:** newline-delimited JSON; request must include `"op"`; optional `"v":1` (missing → 1; unknown → `UNSUPPORTED_VERSION`). Responses: `{ok:true, data}` / `{ok:false, error, code}`.

**Auth:** `platform.PeerCredentials` (Linux `SO_PEERCRED`, Darwin `LOCAL_PEERCRED` with pid=-1). Allow uid 0 or membership in service group. Escape hatches: `--allow-unauth` / `AllowAllAuth`, or env `MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH=1` when peercred is unavailable (default deny).

**Ops (via `HandleRequest`):** `status` (`include_sizes?`), `metrics`, `config_get`, `config_set`, `rescan`, `retry`, `mount`, `unmount`, `purge`, `hooks_*`, `reload`, `stop`.

### Phase 8 — service loop + doctor

| Package | Responsibility |
|---------|----------------|
| `internal/service` | Daemon lifecycle: pidfile flock, service dirs, boot remount, control socket, inotify hint, signal stop/reload, single-threaded `Tick`, `HandleRequest` control map, status payload via `internal/status` |
| `internal/doctor` | Offline environment diagnostics: host/WSL/FUSE/unmount tool, `user_allow_other`, Go + ratarmount(-rs) + archiveconverter + 7z bins, service paths/source dirs, index DrvFs layout, free-space, peercred/control socket notes, systemd PID1 + drop-in generation, service-user messaging |

**CLI:** `mount-wrapper serve [--config] [--once] [--allow-unauth]` plus socket-backed ops (Phase 7.2).

**Still deferred:** real FUSE CI; stream-flatten; full solid-folder parse (CLI probe only).

**Doctor API:** `Run(Options) *Report` with injectable probes. Formatters: `FormatText`, `FormatJSON`, `Report.ToMap`. Systemd: `BuildSystemdDropin` / `ApplyFixSystemd`.

**Hard fail:** only `severity=error` + `ok=false`. Missing optional tools / FUSE are **warn**.

**Wired:** CLI `doctor`, `GET /api/doctor` (Phase 9). SPA doctor panel still Phase 10.

### Phase 9 — HTTP API + SSE

| Package | Responsibility |
|---------|----------------|
| `internal/api` | Localhost HTTP (`net/http`), optional Bearer `web_token` (+ `?token=` for GET), REST via `Backend.HandleRequest`, in-process doctor/wsl-info, SSE `/api/events` (snapshot + deltas + heartbeat), embedded SPA from `internal/webui` |
| `internal/service` | Starts API when `web_enabled`; `APIBackend` adapter; stops on `Shutdown`; `SkipWeb` for tests |

**Enable:** `web_enabled: true`, bind `web_host`:`web_port` (default `127.0.0.1:8787`). Non-loopback bind logs a warning.

**Auth:** empty `web_token` → open API. Non-empty → `Authorization: Bearer …` or `?token=` on GET.
**Prometheus exception:** `GET /metrics` is always open when bind is loopback (token optional for scrapers).

**Rate limits:** POST `/api/purge`, `/api/unmount` with `all: true`, and `/api/rescan` use a per-client-IP min interval (default 2s). Excess → HTTP 429 `RATE_LIMITED`. See [security.md](./security.md).

| Method | Path | Notes |
|--------|------|--------|
| GET | `/api/health` | web ok + serve reachable + pid/version |
| GET | `/api/status`, `/api/status/sizes` | control status; sizes = `include_sizes` |
| GET | `/api/archives` | status + metrics merge + counts/summary |
| GET | `/api/metrics` | query: `archive_id`, `no_cache`, `prefer_mount` |
| GET/POST | `/api/config` | get snapshot; set `config`/`patch` + `apply` |
| POST | `/api/rescan`, `/unmount`, `/retry`, `/purge` | control ops; purge / unmount-all / rescan rate-limited |
| GET | `/api/hooks` | no `archive_id` → control `hooks_list`; `?archive_id=` → `hooks_status` |
| GET | `/api/doctor` | in-process `doctor.Run` |
| GET | `/api/wsl-info` | UNC hint from `WSL_DISTRO_NAME` |
| GET | `/api/events` | SSE deltas (see below) |
| GET | `/metrics` | Prometheus text exposition (always when web on; not under `/api/*`) |
| GET | `/`, `/settings`, `/assets/*` | embedded SPA + client-route fallback |

#### Prometheus `GET /metrics`

Hand-written Prometheus text format (no `prometheus/client_golang` dependency).
Registered whenever the HTTP server is running (`web_enabled`); no separate
config flag.

| Metric | Type | Notes |
|--------|------|--------|
| `mount_wrapper_archives{status=…}` | gauge | Counts per lifecycle status (`discovered`…`absent`) |
| `mount_wrapper_low_disk` | gauge | `0`/`1` from status `low_disk` |
| `mount_wrapper_last_scan_timestamp_seconds` | gauge | Unix time of last discovery scan (`0` if never) |
| `mount_wrapper_info{version=…}` | gauge | Always `1` |

**Auth:** loopback bind (`127.0.0.1` / `::1` / `localhost`) → scrape without
token (even if `web_token` is set). Non-loopback bind → same Bearer / `?token=`
rules as `/api/*`. See [security.md](./security.md).

#### SSE `/api/events`

Auth matches other `/api/*` routes. Stream is `text/event-stream`.

| Event | When |
|-------|------|
| `snapshot` | On connect (full status); again every `SSEFullSnapshotEvery` refresh ticks (default **4**) for resync |
| `counts` | Overview counts / top-level count fields / `low_disk` / `last_scan_at` change |
| `archive` | One or more rows changed (status, progress, error, paths, …) or removed; payload `{"archives":[…],"removed_ids":[…]}` |
| `scan` | `last_scan_at` moves (includes `last_scan` / duration when present) |
| `low_disk` | `low_disk` boolean edge; may include free/min bytes |
| `metrics` | Optional: `metrics_summary` change when `SSEIncludeSizes` polls status with sizes |
| `heartbeat` | Named event + SSE comment; default every 15s |

Defaults (overridable via `api.ServerOptions`): refresh **2s**, heartbeat **15s**, full snapshot every **4** ticks. Production `APIBackend` implements `ChangeNotifier`: `service.Tick` and mutating control ops call `NotifyChange` (buffered, coalesced) so SSE can wake earlier than the 2s ticker; ticker remains the idle fallback.

**Dev split:** Vite (`make web-dev`) proxies `/api` → `http://127.0.0.1:8787` (see `web/vite.config.ts` and `docs/dev.md`).

### Phase 11 — packaging (in-tree polish)

| Surface | Choice |
|---------|--------|
| Identity | binary / unit / user `mount-wrapper` (D9/D10) |
| Web process | Embedded in `serve` only — no sidecar web unit (D4) |
| systemd | `TimeoutStopSec=300` (unmount pool), `DeviceAllow=/dev/fuse`, `ProtectSystem=strict` + state `ReadWritePaths`, optional `EnvironmentFile=-/etc/mount-wrapper/env` |
| macOS | launchd **user agent**; socket under Caches (path length); macFUSE external |
| Engines | Not bundled; PATH resolve ratarmount-rs / fuse3 / archiveconverter / 7z |
| Rocky | Prefer `CGO_ENABLED=0` pure-Go (release default); optional Alpine musl/static via `make build-musl` / `package-musl` + CI `musl-static-smoke` + release `*_musl.tar.gz` (D7) |
| Release | `.goreleaser.yaml` + `release.yml` + `SHA256SUMS`; optional musl attach; Makefile ldflags → `main.version`/`commit`/`date` |

Operator docs: [install.md](./install.md), [macos.md](./macos.md), [migration.md](./migration.md), [parity.md](./parity.md). Artifacts under `packaging/`. Full deb/rpm CI publish is residual.

### Phase 12 — parity inventories

Offline scripts under `tools/parity/` (`run_all.sh`) regenerate CLI / config-key / socket-op markdown against optional sibling `../tarmount-wsl`. Protocol is **new only** (D6). See [parity.md](./parity.md).
