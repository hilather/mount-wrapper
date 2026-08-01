# Migration guide: tarmount-wsl (Python) → mount-wrapper (Go)

**Soft replace** (decision **D13**): document the cutover; no dual-install tooling and
no requirement to co-install the Python package with the Go daemon.

This is a **behavior-parity rewrite** with clean names, paths, and protocol — not a
drop-in binary rename.

---

## What stays conceptually the same

| Concept | Notes |
|---------|--------|
| YAML schema | Still `version: 1`; public keys largely 1:1 (Appendix D) |
| SQLite logical schema | Migrations **001–006** shape matches Python (D5) |
| Lifecycle statuses | Same status enum + transitions (incl. `converting`) |
| Control op **names** | Familiar (`status`, `metrics`, `config_get`, …) |
| Hooks model | First-mount scripts in `hooks.d`, exit `0` / `75` / other |
| Engines | External ratarmount-rs / archiveconverter / 7z |

---

## What deliberately changes

### Paths and identity (D2, D9, D10)

| Surface | tarmount-wsl | mount-wrapper |
|---------|--------------|---------------|
| Binary / package / unit | `tarmount-wsl` | **`mount-wrapper`** only |
| Service user/group | (package-specific) | **`mount-wrapper` / `mount-wrapper`** |
| Config | `/etc/tarmount-wsl/config.yaml` | `/etc/mount-wrapper/config.yaml` |
| State / mounts / indexes | `/var/lib/tarmount-wsl/…` | `/var/lib/mount-wrapper/…` |
| Control socket / pid | `/run/tarmount-wsl/…` | `/run/mount-wrapper/…` |
| Hooks dir | `/etc/tarmount-wsl/hooks.d` | `/etc/mount-wrapper/hooks.d` |
| Env file (systemd) | package-specific | `/etc/mount-wrapper/env` (optional) |

Update `source_dirs`, custom path overrides, and any external automation that
hard-coded old paths.

### State database (D5)

- **Same logical schema** as Python migrations 001–006 (easier mental model).
- **New default path:** `/var/lib/mount-wrapper/state.db`.
- **No auto-open** of an old `/var/lib/tarmount-wsl/state.db`.
- Serve does **not** migrate or reattach the Python DB by default.

**Optional manual reuse (advanced):** if you copy an old DB to the new path, ensure
schema versions match and **stop** the Python service first. Prefer a clean
re-discover (scan sources, rebuild indexes) for production cutover.

Indexes and overlays under the old tree are **not** auto-imported. Point
`index_dir` / `overlay_dir` / `mount_root` at new locations or copy deliberately.

### Control socket protocol (D6)

- **New protocol only** — no dual-protocol / dual-daemon guarantee.
- Framing: newline-delimited JSON, `"v": 1`.
- Auth: peercred **root or group `mount-wrapper`** (not the old package group).
- Escape hatch env: **`MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH`** (not `TARMOUNT_…`).

Do not point `tarmount-wsl` CLI at a mount-wrapper socket or the reverse.

### Hooks environment (D3)

| Upstream | mount-wrapper |
|----------|---------------|
| `TARMOUNT_ARCHIVE_ID`, `TARMOUNT_MOUNT_PATH`, … | **`MOUNT_WRAPPER_ARCHIVE_ID`**, **`MOUNT_WRAPPER_MOUNT_PATH`**, … |

There is **no** dual export of `TARMOUNT_*`. Rewrite hooks before cutover.

Argv is still: `hook <mount_path> <archive_path>` (plus name discovery under `hooks.d`).

### Web UI (D4)

| Upstream | mount-wrapper |
|----------|---------------|
| Sidecar `tarmount-wsl web` (+ optional web unit) | **Embedded in `serve`** when `web_enabled: true` |
| Vanilla JS dashboard | Svelte 5 SPA + **SSE** (poll fallback) |

Default bind remains loopback **`127.0.0.1:8787`**. Optional `web_token` Bearer.

### Mount backend default (D14)

| Upstream default | mount-wrapper default |
|------------------|------------------------|
| **python** ratarmount (deprecated) | **`rust` only** (`ratarmount-rs`) |

Engines are **not** vendored inside the Go package. Install `ratarmount-rs` (or
ratarmount-rs on `PATH` for the service user.

### Packaging stance

- No private Python ratarmount venv inside mount-wrapper packages.
- systemd unit includes **`TimeoutStopSec=300`** (unmount pool).
- Soft replace: stop and disable `tarmount-wsl` / `tarmount-wsl-web` before enabling mount-wrapper if both would fight over sources or FUSE mounts.

---

## Suggested cutover procedure

1. **Inventory** current Python config, hooks, source dirs, and whether DrvFs paths are used.
2. **Install engines** on the target host (`fuse3` / macFUSE, `ratarmount-rs`, optional archiveconverter / 7z). See [install.md](./install.md).
3. **Install** `mount-wrapper` binary + `packaging/scripts/create-user.sh` (or package).
4. **Write config** from `packaging/examples/config.yaml.example`:
   - Copy `source_dirs`, mount/convert policy from old YAML.
   - Fix paths to `/etc|var|run/mount-wrapper/…`.
   - Set `mount_backend` / bins as needed.
5. **Port hooks** to `MOUNT_WRAPPER_*` env names; install under `/etc/mount-wrapper/hooks.d/`.
6. **Stop** Python service(s); unmount any leftover FUSE mounts if needed.
7. **Start** mount-wrapper (`systemctl enable --now mount-wrapper` or launchd).
8. **Verify:**
   - `mount-wrapper doctor --json`
   - `mount-wrapper status` / dashboard `http://127.0.0.1:8787/`
   - Rescan; confirm first archive indexes/mounts; hooks once on first success.
9. **Deprecate** old unit files and package when stable (see below).

For platform manual checklists (WSL / Rocky / macOS), see [parity.md](./parity.md).

---

## Deprecation notes (tarmount-wsl)

When mount-wrapper is the operator path:

| Step | Action |
|------|--------|
| Soft freeze | Stop shipping new features to Python tree; security-only if still published |
| Package | Mark `tarmount-wsl` package as superseded by `mount-wrapper` in release notes |
| Units | Disable `tarmount-wsl.service` and any `tarmount-wsl-web` sidecar |
| Paths | Leave old `/var/lib/tarmount-wsl` for a retention window; then archive/delete |
| Hooks | Remove dual-env shims if any were added during transition |
| Docs | Point README / wiki at mount-wrapper; keep this migration page |

There is **no** automated uninstall coupler between the two packages (D13).

---

## Quick reference — env and names

| Item | Value |
|------|--------|
| Module | `github.com/hilather/mount-wrapper` |
| Binary | `mount-wrapper` |
| Config | `/etc/mount-wrapper/config.yaml` |
| Socket | `/run/mount-wrapper/control.sock` |
| Hooks env prefix | `MOUNT_WRAPPER_` |
| Control unauth escape | `MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH` |
| Default mount backend | `rust` |

Related: [parity.md](./parity.md), [architecture.md](./architecture.md), [install.md](./install.md), decisions log in [TODO.md](../TODO.md).
