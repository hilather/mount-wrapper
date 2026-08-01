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
- Connection badge connected; Archives table updates
- Rescan / Doctor panel
- Settings validate dry-run

### Nested automount skips (operator surface)

When a mounted outer archive has nested members ratarmount-rs skipped:

- Status JSON / SSE archive row: `nested_skips_count`, `nested_skips_summary` (and often `last_error` = pure summary on mounted success)
- SPA Archives: warn chip (`N nested skip(s)`) + subtitle under status for **mounted** rows; failed rows show full `last_error` (enriched with skip segment when present)
- Logs: `event=nested_archive_skipped` per path; `event=index_nested_skipped` summary

Quick check:

```bash
curl -sS http://127.0.0.1:8787/api/status | jq '.archives[] | select(.nested_skips_count != null) | {archive_basename, status, nested_skips_count, nested_skips_summary, last_error}'
```

Prometheus: `curl -sS http://127.0.0.1:8787/metrics | head`

## File bugs for v0.1.2

Capture at least:

- OS + arch + package vs tarball  
- Config snippet (redact paths if needed)  
- Engine binary versions  
- Log lines around `event=` / `last_error`  
- Repro archive class (DrvFs path, solid 7z, zip nested, large index)

## CI coverage map

| Workflow | What |
|----------|------|
| `ci.yml` | Ubuntu unit tests, race subset, build; **macOS** unit tests + build + binary smoke (`macos-unit-smoke`, no macFUSE); web check/test/build |
| `smoke.yml` | Ubuntu binary smoke + Rocky 8 container smoke |
| `smoke.yml` dispatch `run_fuse` | Optional FUSE test (Linux) |
| `release.yml` | Multi-arch publish on `v*` tags (CGO=0 + optional `*_musl.tar.gz`) |
