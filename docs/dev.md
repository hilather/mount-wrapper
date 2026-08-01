# Development

## Prerequisites

- Go 1.25+ (module requires 1.25 for pure-Go SQLite; CI uses `1.25.x`)
- Node 22+ (for SPA)
- Optional: golangci-lint, fuse3 + ratarmount-rs (for optional FUSE integration tests)

## CI (GitHub Actions)

| Workflow | Jobs |
|----------|------|
| [`ci.yml`](../.github/workflows/ci.yml) | Ubuntu unit tests + race subset + build; **macOS** unit tests + build + binary smoke (`macos-unit-smoke`); web check/test/build; optional Playwright dispatch |
| [`smoke.yml`](../.github/workflows/smoke.yml) | Ubuntu binary smoke + Rocky 8 container smoke + **package-contents-smoke** (nfpm deb inventory) + **musl-static-smoke** (Alpine build); optional FUSE dispatch |
| [`release.yml`](../.github/workflows/release.yml) | Multi-arch publish on `v*` tags (CGO=0 goreleaser + optional musl tarballs) |

**macOS CI scope:** `macos-latest` runs `go test ./...`, `CGO_ENABLED=0 make build`, and
`scripts/smoke-binary.sh` (version / help / doctor / config show / serve --once).
It does **not** install or require **macFUSE**; real mount/unmount remains local/manual
([macos.md](./macos.md)). Unix-specific or FUSE-gated tests skip or use stubs as designed.

Security notes for operators: [security.md](./security.md). Field-test: [field-test.md](./field-test.md). Man page: `packaging/man/mount-wrapper.1`.

## Quick start

```bash
# Go
make test          # includes TestPackageTarInventory (synthetic tar + PACKAGE_TAR=; no nfpm)
make smoke         # version + doctor + serve --once
# make smoke-package  # nfpm deb path inventory (soft-skip without nfpm/dpkg-deb)
# PACKAGE_TAR=dist/…_linux_amd64.tar.gz SKIP_DEB=1 ./scripts/smoke-package-contents.sh
# make smoke-rocky # docker/podman + rockylinux:8
# make build-musl / smoke-musl  # optional D7 Alpine static path (docker/podman)
# Optional real-FUSE integration (skipped without /dev/fuse or engine on PATH):
#   go test -tags=fuse ./internal/mounter/ -count=1 -run TestRealFUSEMountUnmount -v
#   PATH=/path/to/ratarmount-rs/target/release:$PATH go test -tags=fuse ./internal/mounter/ -count=1
make build
./bin/mount-wrapper version

# Offline CLI (no FUSE / no serve)
./bin/mount-wrapper doctor --json
# ./bin/mount-wrapper doctor --config packaging/examples/config.yaml.example --json
# ./bin/mount-wrapper config show --local --config packaging/examples/config.yaml.example
# ./bin/mount-wrapper config set --config … --patch --json '{"log_level":"DEBUG"}' --dry-run
# ./bin/mount-wrapper reload --config …           # human: reload scheduled
# ./bin/mount-wrapper reload --config … --json    # machine: {"reload":"scheduled"}
# or SIGHUP; applies log_level + hot keys

# Serve one tick (loads config, opens state DB, scan/reconcile/work once, exits)
# Use a writable debug config with source_dirs / state_db under /tmp or a project path.
# ./bin/mount-wrapper serve --config packaging/examples/config.debug.yaml.example --once

# Socket-backed CLI (requires running serve + control socket server — Phase 7.1)
# ./bin/mount-wrapper status --config … --json
# ./bin/mount-wrapper rescan --assume-stable
# Override socket without full config: --socket /path/to/control.sock

# Frontend (HMR; proxies /api → http://127.0.0.1:8787)
make web-install
make web-dev

# Production SPA into embed.FS
make web-build
make build
```

### HTTP API + Vite proxy (Phase 9–10)

1. Enable web in config (`web_enabled: true`, default bind `127.0.0.1:8787`).
2. Run the daemon: `./bin/mount-wrapper serve --config …` (long-running, not `--once`).
3. SPA dev: `make web-dev` — Vite serves the UI on `:5173` and proxies `/api/*` (including SSE `/api/events`) to the daemon. Proxy timeouts are disabled so EventSource stays open.
4. Optional `web_token`: set in config; SPA injects `window.__MOUNT_WRAPPER_TOKEN__` when assets are served from the daemon. For Vite-only dev, set the token in the browser console (`window.__MOUNT_WRAPPER_TOKEN__ = '…'`) or temporarily leave `web_token` empty on loopback.
5. Production: `make web-build && make build` embeds `web/dist` into `internal/webui`; same origin as the API (no CORS).
6. SPA quality gates:
   - `make web-check` — `svelte-check` + `tsc`
   - `make web-test` — vitest (formatters, connection badge labels, table sort/filter, SSE backoff)
   - `make web-build` — production bundle → `internal/webui/dist`
   - **Optional E2E** (local or Actions `workflow_dispatch`; not default CI):
     ```bash
     cd web
     npm run test:e2e:install          # once: download Chromium
     RUN_E2E=1 npm run test:e2e        # or: make web-e2e
     ```
     Smoke starts **Vite only** (no mount-wrapper daemon), mocks `/api/*` via
     `page.route` (`web/e2e/helpers.ts`):
     - Archives shell: health/status/archives/events → heading + connection badge
       (`live (SSE)` / `poll (SSE down)` / reconnecting; `aria-live`)
       (`web/e2e/smoke.spec.ts`)
     - Archives table + actions: non-empty mounted/`mount_failed` rows; Retry /
       Unmount / Purge / Rescan / Unmount-all POSTs + confirm + toasts;
       nested automount skip chip + subtitle / enriched failed `last_error`
       (`web/e2e/archives.spec.ts`)
     - Doctor panel: `GET /api/doctor` → check names (`web/e2e/doctor.spec.ts`)
     - Settings: `GET`/`POST /api/config` → Sources/Paths groups, Validate dry-run,
       Apply success, sticky `restart_required` banner (`web/e2e/settings.spec.ts`)
     Without `RUN_E2E=1`, `npm run test:e2e` exits 0 (skip) so offline/main CI stay green.

#### Settings: restart-required sticky banner

After **Apply**, when `POST /api/config` returns a non-empty `restart_required`
list (especially `web_enabled` / `web_host` / `web_port` / **`web_token`**), the
Settings page shows a **sticky** warn banner listing those keys.

| Behavior | Detail |
|----------|--------|
| Survives **Validate** | Dry-run only updates the transient status banner |
| Survives **Reload from service** | Sticky list is kept; keys no longer in API `restart_required_keys` are dropped |
| **Dismiss** | Clears sticky list (+ optional `sessionStorage`) |
| Persistence | `sessionStorage` key `mount-wrapper.settings.pendingRestartKeys` (tab-scoped) |
| **Not** live-applied | `web_token` and other `web_*` are captured at **serve start** — write to YAML + reload does **not** rebind HTTP or rotate the Bearer token until process restart |

Do not expect the SPA to call an API that rotates `web_token` in-process; the
banner is the operator signal to `systemctl restart mount-wrapper` (or equivalent).

#### SPA layout (`web/src`)

| Path | Role |
|------|------|
| `App.svelte` | Shell: nav, theme (`mw-theme`), connection badge, auto-refresh |
| `pages/Archives.svelte` | Overview, savings, table, doctor, global actions |
| `pages/Settings.svelte` | Grouped config form, validate/apply, sticky restart banner |
| `lib/api.ts` | `fetchJson` + typed helpers; Bearer from `__MOUNT_WRAPPER_TOKEN__` |
| `lib/api-types.ts` | D11 typed API surface (re-exports from `types.ts`; not OpenAPI codegen) |
| `lib/types.ts` | Hand-written request/response shapes (aligned with [openapi.yaml](./openapi.yaml) schemas) |
| `lib/sse.ts` | EventSource client (all SSE named events), exponential backoff reconnect |
| `lib/connection.ts` | Badge label/title helper: `live (SSE)` vs `poll (SSE down)` |
| `lib/merge.ts` | Archive row merge / fine-grained SSE patch (preserve metrics) |
| `lib/format.ts` | Bytes / duration / status labels |
| `lib/settings-schema.ts` | Public config field groups (Settings form) |
| `lib/pending-restart.ts` | Sticky restart-required keys + sessionStorage helpers |
| `lib/stores/app.svelte.ts` | Shared runes state (archives, connection, toasts) |
| `components/*` | Table, pills, savings, doctor, badge, toasts, hooks drawer |
| `e2e/` | Optional Playwright smoke (mocked API; `RUN_E2E=1`) |

### Parity inventories (Phase 12)

```bash
./tools/parity/run_all.sh   # CLI / config keys / socket ops markdown
go test ./tools/parity/
```

See [parity.md](./parity.md) and [migration.md](./migration.md).

## Layout

| Path | Role |
|------|------|
| `cmd/mount-wrapper` | Binary entry |
| `internal/config` | YAML config (schema v1) — unit-tested |
| `internal/platform` | Host/FUSE/peercred |
| `internal/paths` | WSL/DrvFs path helpers |
| `internal/state` | SQLite lifecycle store + migrations — unit-tested |
| `internal/match` | Archive name regex + extension filter — unit-tested |
| `internal/scanner` | Discovery + stable-file + fingerprint — unit-tested |
| `internal/archives` | archives_dir path helpers + relocate (space gate, sidecar) — unit-tested |
| `internal/convert` | Convert predicates, cmd construction, metadata, zip/flatten runners — unit-tested |
| `internal/mounter` | Backend resolve, cmd build, process group, unmount, Engine convert jobs — unit-tested; optional `//go:build fuse` real mount |
| `internal/reconcile` | Liveness + boot plan — unit-tested |
| `internal/cleaner` | Grace purge + overlay quarantine + outer nonsolid cache hygiene — unit-tested |
| `internal/hooks` | hooks.d discovery, security, runner — unit-tested |
| `internal/metrics` | Space-saved formulas + collector — unit-tested |
| `internal/doctor` | Offline diagnostics report — unit-tested |
| `internal/status` | Rich status payload + human formatter — unit-tested |
| `internal/control` | Unix socket JSON-lines server/client + peercred auth — unit-tested |
| `internal/service` | Serve loop, pidfile, signals, HandleRequest + control socket + optional HTTP — unit-tested |
| `internal/cli` | Operator CLI (offline + socket client) — unit-tested |
| `internal/api` | HTTP API + SSE + SPA static — unit-tested |
| `internal/webui` | `embed.FS` of SPA `dist/` |
| `web/` | Svelte 5 + Vite source |
| `packaging/` | systemd, launchd, examples, create-user, WSL samples, nfpm (local + goreleaser) |
| `testdata/` | Fixtures |
| `docs/` | architecture, dev, install, macOS |
| `AGENTS.md` | Mandatory agent policy (docs + tests + review) |
| `.grok/skills/` | keep-docs-current, keep-tests-current, review-changes |
| `.goreleaser.yaml` | Release binary matrix (`CGO_ENABLED=0`); musl via post-step scripts |

## Version embedding

`make build` injects version metadata (same keys as goreleaser):

```bash
make build
./bin/mount-wrapper version
# LDFLAGS: -X main.version=… -X main.commit=… -X main.date=…
```

Release archives should ship **`SHA256SUMS`** (see `.goreleaser.yaml` checksum block and [install.md](./install.md)).

## Package smoke

```bash
go test ./internal/config/... ./internal/platform/... ./internal/paths/... \
  ./internal/state/... ./internal/match/... ./internal/scanner/... ./internal/archives/... \
  ./internal/mounter/... ./internal/convert/... ./internal/reconcile/... ./internal/cleaner/... \
  ./internal/hooks/... ./internal/metrics/... ./internal/doctor/... ./internal/status/... \
  ./internal/control/... ./internal/service/... ./internal/api/... ./internal/cli/...
# Load example:
# packaging/examples/config.yaml.example
# Smoke serve (oneshot):
# make build && ./bin/mount-wrapper serve --config packaging/examples/config.debug.yaml.example --once
# Offline CLI:
# ./bin/mount-wrapper doctor --config packaging/examples/config.yaml.example --json
# ./bin/mount-wrapper config show --local --config packaging/examples/config.yaml.example
```

## Agent quality bar

Before considering a change complete:

1. **Docs** — update README / TODO / docs / packaging examples for user-visible diffs  
2. **Tests** — add regression coverage for behavior changes (`make test`)  
3. **Review** — run `/review` or the `review-changes` skill; fix bugs  

See [AGENTS.md](../AGENTS.md).

## HTTP API / OpenAPI

Hand-written OpenAPI 3 document (D11 — **not** generated from Go or used for SPA codegen):

| Item | Detail |
|------|--------|
| Spec | [openapi.yaml](./openapi.yaml) (`info.version` tracks API sketch; currently **0.1.3**) |
| Schemas | `components.schemas` for Health, Status, Archive, Metrics, Config, Doctor, Hooks, WSLInfo, ErrorBody, RATE_LIMITED, etc. |
| Sources of truth | `web/src/lib/types.ts`, `internal/api` handlers/SSE, `internal/status`, `internal/doctor`, `internal/metrics` |
| Guard test | `TestOpenAPIDocument` in `internal/api` (loads YAML, asserts paths + schema keys + non–description-only 200s) |
| Residual | Optional OpenAPI → TS client codegen; SPA still uses hand-written `api-types.ts` / `types.ts` |

When adding or renaming `/api/*` paths or JSON fields that the SPA consumes, update **both** the Go/TS types and `docs/openapi.yaml` (and keep `make test` green).
