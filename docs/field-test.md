# Field test checklist (v0.1.x)

Use after installing a release binary or package. Complements [parity.md](./parity.md)
automated inventories and [install.md](./install.md).

## Platforms

| Platform | Install path |
|----------|----------------|
| Ubuntu / Debian | `.deb` or `linux_amd64` tarball |
| Rocky 8+ / RHEL | `.rpm` or same tarball (`CGO_ENABLED=0`; optional `make build-musl` static) |
| WSL2 | Ubuntu package + optional Task Scheduler sample |
| macOS | `darwin_*` tarball + macFUSE + launchd example |

## Smoke (no FUSE)

From a built binary:

```bash
./scripts/smoke-binary.sh --build
# or on Rocky host/container with binary mounted:
./scripts/smoke-rocky8.sh --build
# optional Alpine musl/static path:
# make smoke-musl
```

Expect: `version`, `doctor --json`, `config show --local`, `serve --once` all succeed.

## Real mount (needs engine)

1. Install **ratarmount-rs** and **fuse3** / **macFUSE**.
2. Copy `packaging/examples/config.debug.yaml.example` → a writable config; set `source_dirs`, paths under a temp or `/var/lib/mount-wrapper`.
3. Drop a small `sample.tar.gz` into a source dir.
4. `mount-wrapper serve --config … --allow-unauth` (dev) or systemd unit.
5. `mount-wrapper rescan --assume-stable` then `status --json`.
6. Confirm mount path appears; open files through FUSE; `unmount --all`.

### WSL UNC / `windows_visible`

When testing `\\wsl.localhost\…` visibility:

1. `windows_visible: true` and `user_allow_other` in `/etc/fuse.conf`.
2. `mount-wrapper doctor --json` — checks **`user_allow_other`** and
   **`windows_visible_parent_ox`** should not warn (or apply the printed
   `chmod o+x` fix for custom `mount_root` parents).
3. From Windows Explorer, open the UNC path under the distro to a mounted archive.

Optional FUSE unit test (local):

```bash
go test -tags=fuse ./internal/mounter/ -count=1 -run TestRealFUSEMountUnmount -v
```

## Convert paths (needs 7z)

| Case | How to exercise |
|------|-----------------|
| Nested outer 7z | `testdata/nested7z/SUP-36264-nested-mini.7z` with `convert_7z_nonsolid` + `scope: flatten` |
| Zip with nested archives | Drop a zip containing `.tar.gz`/`.7z` members; `convert_zip_to_7z: true` |
| Solid 7z + archiveconverter | Enable `archiveconverter_*` if the tool is on PATH |

## Web UI

With `web_enabled: true`:

- Open `http://127.0.0.1:8787/`
- Connection badge: **`live (SSE)`** when EventSource is open; **`poll (SSE down)`**
  when HTTP poll is driving the UI (SSE down/not yet connected); **`reconnecting`**
  during SSE backoff. Tooltip explains the mode; badge uses `aria-live="polite"`.
- Archives table updates (SSE deltas when live; poll refresh when poll-only)
- Rescan / Doctor panel
- Settings validate dry-run

### Nested automount skips (operator surface)

When a mounted outer archive has nested members ratarmount-rs skipped:

- Status JSON / SSE archive row: `nested_skips_count`, `nested_skips_summary` (and often `last_error` = pure summary on mounted success)
- SPA Archives: warn chip (`N nested skip(s)`) + subtitle under status for **mounted** rows; failed rows show full `last_error` (enriched with skip segment when present)
- Logs: `event=nested_archive_skipped` per path; `event=index_nested_skipped` summary
- **Remount / index→mount durability:** after unmount + remount (or two-phase index then FUSE), nested-skip advisory should still appear — pure `last_error` summary is persisted at index complete and kept by `MarkMounted` when the FUSE child does not re-emit skip lines. Re-check the same archive row after remount; chip/count must not disappear solely because live `SkippedNested` was empty.

Quick check:

```bash
curl -sS http://127.0.0.1:8787/api/status | jq '.archives[] | select(.nested_skips_count != null) | {archive_basename, status, nested_skips_count, nested_skips_summary, last_error}'
```

Prometheus: `curl -sS http://127.0.0.1:8787/metrics | head`

## Operator surfaces (v0.1.3)

Exercise the post–v0.1.2 operator polish before filing issues:

| Surface | How to check |
|---------|----------------|
| **CLI `reload`** | With serve running: `mount-wrapper reload --config …` → prints `reload scheduled` (exit 0). With `--json` → parseable control ack `{"reload":"scheduled"}`. Or `kill -HUP $(pidof mount-wrapper)`. |
| **CLI `stop`** | With serve running: `mount-wrapper stop --config …` → prints `stop scheduled` (exit 0); serve exits and runs Shutdown. With `--json` → `{"stop":"scheduled"}`. Or `kill -TERM $(pidof mount-wrapper)`. |
| **`log_level` hot-reload** | Set `log_level: DEBUG` in config YAML, `reload`, confirm slog verbosity changes without process restart. Env `MOUNT_WRAPPER_LOG_LEVEL` still overrides while set. |
| **CLI `hooks rerun`** | On a **mounted** archive with terminal `hooks_status=success`: `mount-wrapper hooks rerun ARCHIVE_ID` → human `hooks skipped …` (exit 0). Then `… --force` (or `--json`) → `hooks ran … hooks_status=success` (or JSON `ran:true`). Failed status without `hook_rerun_on_failure` also skips unless `--force`. |
| **SPA hooks force re-run** | Open Archives → row **Hooks** drawer. **Re-run** on terminal success → toast `Hooks skipped` (no force). **Force re-run** → confirm → toast `Hooks ran` (or API error). Same as `POST /api/hooks` `{archive_id, force}`. |
| **SSE deltas** | Web UI open with badge **`live (SSE)`**: change one archive status (rescan / unmount single row) and confirm Archives table patches that row without a full wipe/flash; badge stays `live (SSE)`. Stop/block `/api/events` briefly and confirm badge moves to **`reconnecting`** / **`poll (SSE down)`** while data still refreshes via poll. |
| **Nested skip (remount if known)** | Mount an outer archive that triggers nested automount skips. Confirm chip/summary on **mounted** status. If the archive is already known/mounted, **unmount + remount** (or restart serve with boot remount) and confirm skip advisory still appears after remount/hooks success. |

Also re-check convert paths and Web UI smoke above when those features are in scope.

## File bugs for v0.1.3 (released)

Capture at least:

- OS + arch + package vs tarball  
- Config snippet (redact paths if needed)  
- Engine binary versions  
- Log lines around `event=` / `last_error`  
- Repro archive class (DrvFs path, solid 7z, zip nested, large index)  
- Whether issue is on first mount vs remount / boot remount

## CI coverage map

| Workflow | What |
|----------|------|
| `ci.yml` | Ubuntu unit tests, race subset, build; **macOS** unit tests + build + binary smoke (`macos-unit-smoke`, no macFUSE); web check/test/build |
| `smoke.yml` | Ubuntu binary smoke + Rocky 8 container smoke |
| `smoke.yml` dispatch `run_fuse` | Optional FUSE test (Linux) |
| `release.yml` | Multi-arch publish on `v*` tags (CGO=0 + optional `*_musl.tar.gz`) |
