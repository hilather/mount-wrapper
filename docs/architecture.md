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
| `internal/config` | YAML schema v1 load/validate, duration parse/format, public snapshot, hot/restart key sets, patch merge, atomic write, slog `log_level` apply (`MOUNT_WRAPPER_LOG_LEVEL` env override) |
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

**Nested automount skips:** while a ratarmount-rs child runs, Engine pipes stderr and parses lines matching `Mounting of '…' failed because of: …`. Paths accumulate on `ManagedMount.SkippedNested`. On failure, `MarkFailed` enriches `last_error` with `skipped N nested mounts: path…` (sample paths). On success (`MarkMounted`), when any skips were recorded: summary is logged (`event=index_nested_skipped`), **`last_error` holds the pure skip summary** (operator advisory; no SQLite migration), and status/API archive rows expose `nested_skips_count` + `nested_skips_summary` (from live `ManagedMount` while the FUSE child is registered, else derived from `last_error`). **Durability across index→mount and remount:** `CompleteIndexAndStartMount` drains index-phase stderr, persists the pure skip summary into `last_error` on the indexing→mounting transition, and **carries** `SkippedNested` onto the new FUSE-phase `ManagedMount` (index-phase live is dropped). `MarkMounted` keeps an existing pure nested-skip `last_error` (`IsNestedSkipOnlyLastError`) when live skips are empty (remount / FUSE phase that does not re-emit skip lines). Hooks success preserves a pure nested-skip `last_error` so the SPA warning remains after first-mount hooks. SPA Archives table shows a warn chip + subtitle on mounted rows with skips; failed rows still show full `last_error` (which may include the skip segment).

**Still deferred:** stream-flatten / full solid-folder parse. Flatten + outer-cache probes are best-effort CLI (`7z l -slt`), not ratarmountcore. Optional real-FUSE smoke: `go test -tags=fuse ./internal/mounter/` (skips without `/dev/fuse` or engine on PATH; not in default `make test`).

#### Windows visibility / parent traverse (Linux + WSL)

When `windows_visible: true`, FUSE mounts use `-o allow_other` so Windows UNC
(`\\wsl.localhost\<distro>\…`) and other local users can open the mount. That
requires:

1. **`user_allow_other`** in `/etc/fuse.conf` (`create-user.sh` enables when possible; doctor check **`user_allow_other`** warns when missing).
2. **Other-executable parents** on the path to each mountpoint: every directory
   from `/` down through `mount_root` (and typically `…/mounts`) needs `o+x`
   (world traverse). FUSE content is still gated by the mount; only path
   resolution needs `x` on parents. Doctor check **`windows_visible_parent_ox`**
   (config present) walks existing ancestors on Linux when `windows_visible` is
   true and **warns** with a `chmod o+x …` fix hint when any dir lacks other-execute;
   details include `missing_ox`, `modes`, and `fix_hint`. macOS emits info only
   (WSL-oriented); `windows_visible: false` is info “not required”.
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
| Outer nonsolid cache | Scope `outer`/`all`: mount path calls `EnsureNonsolidCachedCopy` — solid → CLI extract + `a -ms=off` under content-keyed cache dest; exclusive `{cacheKey}.lock` flock serializes concurrent populate; non-solid only when `7z l` succeeds and `Solid != +` (list fail/empty fail-closed); post-populate `FlattenMinOKSize` floor; encrypted list/extract → `Encrypted7zMessage`; leftover `*.nonsolid.partial` / `*.work` cleaned before populate. On successful resolve to a cache path (`mountArchive != archivePath`), the mount **claim** `Transition` persists `convert_source_size_bytes` / `convert_duration_seconds` when those store columns are still nil: prefer `convert.ReadConvertMetadata(mountArchive)` (sidecar written on populate); fall back to `Stat(source)` for original size only — **no invented duration** on cache hits without a sidecar |

**Engine convert order** (parity with Python `_run_convert`): archiveconverter (if available / `.7z` / not zip-repack) → zip repack → flatten. Success updates `archive_path` + fingerprint, leaves `converting` → `discovered`, then continues to mount/index. Failure → `index_failed` / `mount_failed` with `last_error`. Outer/all cache populate runs at **mount** start (not the convert job), like Python `resolve_mount_archive_path`.

**Residual gaps:**

| Gap | Detail |
|-----|--------|
| Solid/nested probe | Best-effort `7z l -slt` only — not ratarmountcore solid-folder parse; encrypted detect is CLI phrase/`Encrypted=+` only; inject `NeedsFlatten` to override |
| Flatten depth | Best-effort CLI: extract, walk nested `*.7z`, repack `-ms=off`; encrypted refused clearly; **no stream-flatten** / post-rebuild nested-header check |
| Outer cache | CLI extract+repack only (no stream-repack / stream-flatten); exclusive flock on `{cacheKey}.lock` with re-check hit inside lock; fail-closed list + size floor + leftover partial/work cleanup; nested `.7z` members not expanded in outer cache (child env still used for nested when scope allows); store convert columns filled on mount claim from cache sidecar (or source size fallback) so status/SPA stay durable without relying only on live sidecar reads |
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
| `internal/cleaner` | Grace purge of `absent` rows past `cleanup_after` (`ListAbsentPastGrace` + `PurgeArchive`), overlay policy (`quarantine` / `delete` / `retain`), quarantine age + max-bytes prune, admin immediate purge, stale mount-dir cleanup under `mount_root`, outer nonsolid cache hygiene under `convert_7z_cache_dir`, optional ratarmount `/tmp/.tmp*` prune, `min_free_bytes` disk check |

**Path safety:** index/overlay/mount deletes and quarantine moves are refused unless the path resolves under `index_dir`, `overlay_dir`, or `mount_root` respectively. Nonsolid cache deletes are refused unless under the resolved cache root (`convert_7z_cache_dir` or default). Paths outside roots are left on disk; DB row may still be purged so rediscovery is not blocked.

**Outer nonsolid cache hygiene** (`PruneNonsolidCache`, part of `Run`): under `DefaultNonsolidCacheDir` only (direct children).

| Action | Policy |
|--------|--------|
| Leftover partials | Always remove `*.nonsolid.partial` and `*.nonsolid.partial.work` (crashed populate residue) |
| Stale locks | Remove `*.lock` only when sibling `*.7z` is missing; skip if exclusive flock is held (live populate) |
| Age prune | Orphaned `*.7z` with mtime older than **`cleanup_after`** (reused; no separate key), plus sibling `*.tarmount-convert.json` and `.lock`; skip paths listed in `LivePaths` (serve includes live mount dirs **and** `Request.ArchivePath`, so outer-cache mount sources are protected) |

Age is mtime-based (not tied to source archive presence). Cache keys are content-hashed, so entries become “orphan” when unused long enough — same grace window as absent-row purge. Next mount of a pruned key re-runs `EnsureNonsolidCachedCopy`.

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

**Wired:** control `metrics` op via `MetricsCollector` + store `ArchiveSource` adapter in service. Production `New` sets `Collector.Meta` to `ConvertSidecarMeta{Config}` (`convert.ReadConvertMetadata` on `archive_path`, then outer nonsolid cache dest when configured); store convert columns still win when both are set (`ComputeArchiveMetrics` / `ResolveConvertFields`). Outer/all mount claim also copies convert size/duration into the store when nil (see outer nonsolid cache row above) so SPA/status do not depend solely on sidecar presence. CLI metrics surface still optional; status `include_sizes`, control `metrics`, HTTP/SPA consume the collector.

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
| `internal/service` | Daemon lifecycle: pidfile flock, service dirs, boot remount, control socket, inotify hint, signal stop/reload, single-threaded `Tick`, `HandleRequest` control map, status payload via `internal/status`; on reload: re-apply `log_level` (env override), rematerialize scanner sources, restart/stop inotify when `use_inotify` / `source_dirs` change |
| `internal/doctor` | Offline environment diagnostics: host/WSL/FUSE/unmount tool, `user_allow_other`, Go + ratarmount(-rs) + archiveconverter + 7z bins, service paths/source dirs, index DrvFs layout, free-space, peercred/control socket notes, **`web_bind_security`** (non-loopback + empty `web_token` → warn), **`windows_visible_parent_ox`** (Linux: mount_root ancestors lack o+x when `windows_visible` → warn + `chmod o+x` hint), **`convert_cache_dir`** / archiveconverter output writability when convert features are on, Darwin **`control_socket_path_length`** (~100-byte sun_path warn), systemd PID1 + drop-in generation, service-user messaging |

**CLI:** `mount-wrapper serve [--config] [--once] [--allow-unauth]` plus socket-backed ops (Phase 7.2), including `reload`.

#### Hot reload vs restart (serve)

| Applied on `reload` / SIGHUP / `config_set` apply | Requires process restart |
|--------------------------------------------------|---------------------------|
| `log_level` (slog via `LevelVar`; `MOUNT_WRAPPER_LOG_LEVEL` env overrides while set) | `web_enabled`, `web_token`, `web_host`, `web_port` (HTTP bind + token captured at serve start) |
| `source_dirs`, `use_inotify` (inotify watcher restarted/stopped), discovery knobs | Path / DB / socket / hooks_dir / mount_backend / engine bin paths |
| Concurrency, cleanup, hooks, convert, ratarmount engine args, … (see `HotReloadKeys`) | See `RestartRequiredKeys` in `internal/config/public.go` |

Control op `reload` schedules work on the next tick (or immediate via `config_set`). CLI: `mount-wrapper reload`.

**Still deferred:** real FUSE CI; stream-flatten; full solid-folder parse (CLI probe only).

**Doctor API:** `Run(Options) *Report` with injectable probes. Formatters: `FormatText`, `FormatJSON`, `Report.ToMap`. Systemd: `BuildSystemdDropin` / `ApplyFixSystemd`. Inventory contract: `CoreCheckNames` + `CheckName*` in `internal/doctor/inventory.go` (frozen by `TestDoctorCheckInventory`).

**JSON shape (`ToMap` / `FormatJSON` / `GET /api/doctor`):** root keys always `ok`, `checks`, `config_path` (JSON `null` when empty), `notes`, `fixes_applied` (arrays, never null). Each check always has `name`, `ok`, `severity` (`info`|`warn`|`error`), `message`, `details` (object, never null). Structural golden: `TestDoctorFormatJSONStructural` (key sets + severity policy + gated names; not full message goldens). OpenAPI `DoctorReport` / `DoctorCheck` and SPA `web/src/lib/types.ts` mirror this.

**Hard fail:** only `severity=error` + `ok=false`. Missing optional tools / FUSE / open non-loopback web bind / missing parent `o+x` for `windows_visible` / convert cache path issues / long Darwin control socket are **warn** (report still `ok: true`).

**Doctor check inventory (Run order):**

| When | Check names |
|------|-------------|
| Always (no config required) | `go_version`, `host_platform`, `peercred`, `fuse_device`, `fusermount`, `user_allow_other`, `ratarmount_bin`, `archiveconverter`, `sevenzip_bin`, `mount_backend`, `systemd_pid1`, `service_user` |
| Config present | `path.mount_root`, `path.index_dir`, `path.overlay_dir`, `path.control_socket_dir` (if socket set), **`windows_visible_parent_ox`**, `source_dirs` / `source_dirs[i]`, `index_layout`, `disk.*` (mount/index/overlay; deduped), **`web_bind_security`**, **`config`** |
| Convert on (`convert_7z_nonsolid` or `convert_zip_to_7z`) | **`convert_cache_dir`** |
| `archiveconverter_enabled` + output dir set | **`path.archiveconverter_output_dir`** |
| Darwin + config | **`control_socket_path_length`** (~100-byte sun_path warn) |
| `--fix-systemd` | **`fix_systemd`** |

**Wired:** CLI `doctor`, `GET /api/doctor` (Phase 9). SPA doctor panel (Phase 10) consumes these names; e2e mock in `web/e2e/helpers.ts` uses the same IDs.

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

**SPA client** (`web/src/lib/sse.ts` + `web/src/lib/stores/app.svelte.ts`):

| Event | Client behavior |
|-------|-----------------|
| `snapshot` | Full apply via `applySnapshot`; `mergeArchiveRows` preserves per-row `metrics` when status rows omit sizes |
| `counts` | Overview pills / top-level counts (+ `low_disk` / `last_scan_at` when present) without wiping the table |
| `archive` | `patchArchiveRows` upserts by `archive_id`, drops `removed_ids`; metrics preserved on partial status patches |
| `scan` | Updates `last_scan_at` only |
| `low_disk` | Updates low-disk badge flag |
| `metrics` | Sets `metrics_summary` (explicit `null` clears; omit leaves previous) |
| `heartbeat` | Keeps connection healthy (no state wipe) |

Reconnect uses exponential backoff; ~15s poll + occasional `/api/archives` soft-refresh keeps size columns warm while SSE is primary. Pure merge helpers live in `web/src/lib/merge.ts`.

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

Operator docs: [install.md](./install.md), [macos.md](./macos.md), [migration.md](./migration.md), [parity.md](./parity.md). Artifacts under `packaging/`. Deb/rpm (+ optional musl tarballs) publish on `v*` tags via `release.yml`.

### Phase 12 — parity inventories

Offline scripts under `tools/parity/` (`run_all.sh`) regenerate CLI / config-key / socket-op markdown against optional sibling `../tarmount-wsl`. Protocol is **new only** (D6). See [parity.md](./parity.md).
