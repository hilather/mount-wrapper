# Parity verification & cutover

How to inventory **mount-wrapper** against local upstream **tarmount-wsl**, what is
already done, and residual gaps. Complements [migration.md](./migration.md).

---

## Run inventories (offline-safe)

Scripts live under [`tools/parity/`](../tools/parity/). They **always** emit the
Go-side inventory. If sibling `../tarmount-wsl` is missing, they note the skip
and still write markdown.

```bash
# from mount-wrapper repo root
export PATH="$HOME/.local/go/bin:$PATH"

./tools/parity/run_all.sh
# or individually:
# ./tools/parity/cli_surface.sh
# ./tools/parity/gen_config_keys.sh   # needs `go run` for PublicKeys dump
# ./tools/parity/socket_ops.sh
```

Optional:

```bash
export TARMOUNT_WSL_ROOT=/path/to/tarmount-wsl   # default: ../tarmount-wsl
export PARITY_OUT=/tmp/parity-out                # default: tools/parity
```

| Artifact | Contents |
|----------|----------|
| [`tools/parity/cli_surface.md`](../tools/parity/cli_surface.md) | CLI commands both sides + intentional diffs |
| [`tools/parity/config_keys.md`](../tools/parity/config_keys.md) | `config.PublicKeys()` vs upstream parse |
| [`tools/parity/socket_ops.md`](../tools/parity/socket_ops.md) | Control ops + **D6** note |
| [`tools/parity/feature_checklist.md`](../tools/parity/feature_checklist.md) | README feature rows → done/partial/deferred |

Automated checks (no FUSE):

```bash
go test ./tools/parity/          # bash -n scripts + listkeys smoke
go test ./internal/config/ -run PublicKeys
make test
```

---

## Protocol decision (D6)

| Choice | Detail |
|--------|--------|
| **New protocol only** | No dual-protocol adapter; no guarantee that Python CLI talks to Go daemon |
| Familiar ops | Same op **names** where practical (`status`, `metrics`, `config_get`, …) |
| Framing | Newline JSON, `"v": 1` |
| Auth / paths | Group `mount-wrapper`, socket under `/run/mount-wrapper/` |

Documented in Phase 12 / decisions log; see socket inventory for the live op list.
Go currently also implements **`stop`** (graceful serve shutdown); confirm against
upstream inventory when regenerating `socket_ops.md`.

---

## Current gap summary (honest residuals)

These are **true** remaining gaps as of Phase 12 tooling cutover — not checkbox
rot. Prefer fixing code over changing this list without evidence.

| Gap | Severity | Notes |
|-----|----------|-------|
| **Flatten / convert edge paths** | medium | Built-in 7z flatten & zip repack library+runners exist; nested7z/nestedzip fixtures + real-`7z` probes when on PATH; **no stream-flatten**; engine/serve convert edges residual |
| **Real FUSE integration tests** | low for CI | Optional `//go:build fuse` test; optional Actions job `smoke.yml` → `run_fuse` (not on every PR) |
| **Rocky 8 binary smoke** | done in CI | `smoke.yml` builds CGO=0 binary on Ubuntu, runs `scripts/smoke-rocky8.sh` in `rockylinux:8` |
| **macOS unit + binary smoke** | done in CI | `ci.yml` → `macos-unit-smoke` on `macos-latest`: `go test ./...`, CGO=0 build, `scripts/smoke-binary.sh`; **no macFUSE** (real mount remains local/manual) |
| **Musl/static path (D7)** | done | `make build-musl` / `package-musl`; CI `musl-static-smoke`; `release.yml` attaches `*_linux_*_musl.tar.gz` after GoReleaser (primary matrix stays `CGO_ENABLED=0`) |
| **Full deb/rpm CI publish** | done on tags | `release.yml` publishes deb/rpm/tarballs on `v*` (+ optional musl tarballs); postinstall creates user/dirs |
| **Config.yaml package seed** | done | `seed-config.sh` via postinstall: copies example → `/etc/mount-wrapper/config.yaml` only if missing (never overwrites) |
| **Homebrew tap** | residual | Formula sketch under `packaging/homebrew/` is **usable** (`brew install --formula` after `scripts/update-homebrew-formula.sh` fills `SHA256SUMS`); **tap** publish / CI brew install still residual |
| **Playwright SPA smoke** | optional / local | Landed: `web/e2e` mocked `/api/*` — Archives shell, table rows + Retry/Unmount/Purge/Rescan/Unmount-all, Doctor panel checks, Settings validate/apply; `RUN_E2E=1` / optional CI job |
| **OpenAPI / generated SPA client** | optional residual | Hand-written OpenAPI schemas in `docs/openapi.yaml` (richer; version 0.1.6) + hand-written TS (`api-types.ts` + `types.ts`, D11); **codegen / generated SPA client still residual** (keep open) |
| **Windows parent `o+x` traverse** | done (doctor) | Docs + packaging `create-user.sh` o+x; doctor **`windows_visible_parent_ox`** warns on Linux when `windows_visible` and ancestors lack o+x (`chmod o+x` hint); macOS info-only |
| **Control socket live reachability** | done (doctor) | Doctor **`control_socket_live`**: stat + short `status` dial when `control_socket` set; missing serve / dial fail / `PERMISSION_DENIED` → warn (group hint); ok → info with version; never hard-fail |
| **Pidfile / systemd unit liveness** | done (doctor) | Doctor **`pidfile_live`** (`pid_file` set: stat + PID + process alive) and **`systemd_unit`** (Linux + systemd PID1: `systemctl is-active`/`is-enabled` for `mount-wrapper.service`); offline → warn only |
| **launchd agent liveness** | done (doctor) | Doctor **`launchd_agent`** (Darwin: `launchctl list`/`print` for `com.hilather.mount-wrapper`); not loaded / missing launchctl / unclassifiable → warn; clear loaded shape → info; never hard-fail |
| **Prometheus metrics endpoint** | done | `GET /metrics` hand-written text; loopback open / non-loopback token; aggregate size/savings gauges from `metrics_summary` |
| **Separate `web` CLI** | n/a | Embedded serve (D4) by design |

Orchestrator overhead for large archives remains dominated by the **engine** index
cost; that is expected and not a cutover blocker.

---

## Status / API / SPA shape

| Surface | Parity notes |
|---------|----------------|
| Status JSON | Counts incl. `converting`, progress `elapsed_s` / `progress_label`, `indexing_archives`, `low_disk`, optional sizes |
| Metrics | Per-archive + summary; convert source size / duration / delta |
| HTTP API | `/api/health`, status, archives, metrics, config, hooks (GET list/status + POST run), rescan/unmount/retry/purge, doctor, wsl-info; Prometheus `GET /metrics` |
| SSE | `snapshot` / `counts` / `archive` / `scan` / `low_disk` / `metrics` / `heartbeat` + poll fallback |
| SPA (Appendix E) | Overview pills, savings, full metrics columns, filter/sort, row actions, hooks drawer, doctor, theme, UNC, toasts, SSE badge, settings schema |

Regenerate config key inventory after adding public YAML keys so SPA
`settings-schema.ts` and Appendix D stay aligned. Automated guard:
`go test ./internal/config/ -run SettingsSchemaMatchesPublicKeys`.

---

## Manual platform checklists

Short practical lists (no automation required). Expand with host-specific notes
as you run them.

### WSL2 (Ubuntu primary)

- [ ] `doctor --json`: FUSE, ratarmount-rs on PATH for service user, source dirs readable
- [ ] Config `source_dirs` under `/mnt/<drive>/…` (DrvFs); reject UNC `\\wsl$` as **source**
- [ ] Indexes under Linux FS (`index_dir` not on DrvFs) unless `allow_indexes_on_drvfs`
- [ ] Mount with `windows_visible` / `allow_other` when UNC access needed; `user_allow_other` if required; doctor `windows_visible_parent_ox` clean (or fix with `chmod o+x` on parents)
- [ ] Dashboard UNC hint / copy works (`GET /api/wsl-info`)
- [ ] Boot remount: enable systemd unit **or** Task Scheduler example (`packaging/windows-task-scheduler.xml.example`) so WSL distro starts service
- [ ] After reboot: previously mounted archives re-queue without re-running terminal-success hooks
- [ ] `TimeoutStopSec=300` unit in place so stop can unmount pool

### Rocky 8 / RHEL-like

- [ ] Install binary built with **`CGO_ENABLED=0`** (release default) or optional `make build-musl` static — glibc 2.28 constraint
- [ ] `fuse3` / `/dev/fuse`; `modprobe fuse` if needed
- [ ] Install `ratarmount-rs` + optional p7zip / archiveconverter
- [ ] `create-user.sh` + config + systemd unit; `systemctl enable --now mount-wrapper`
- [ ] `doctor`; mount a small **tar.gz** and **zip**; confirm status + metrics
- [ ] Logs via journald; stop unit unmounts cleanly within TimeoutStopSec

### macOS (macFUSE)

- [ ] Install macFUSE; approve system extension
- [ ] Config from `packaging/examples/config.yaml.macos.example` (Caches run dir / short socket path)
- [ ] launchd user agent example loads; service user is login user path profile
- [ ] `doctor`; mount and unmount one archive via CLI/dashboard
- [ ] Peercred / control socket works without unauth escape hatch in normal use
- [ ] Friend-test items in [macos.md](./macos.md)

---

## Related docs

| Doc | Role |
|-----|------|
| [migration.md](./migration.md) | Python → Go cutover + deprecation |
| [install.md](./install.md) | Packages, paths, engines |
| [architecture.md](./architecture.md) | Runtime layout, SSE, packaging summary |
| [dev.md](./dev.md) | Developer workflow |
| [TODO.md](../TODO.md) | Phases, decisions D1–D15, Appendix D/E |
