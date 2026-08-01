# Security review — mount-wrapper

Operator-oriented notes on trust boundaries, defaults, and escape hatches.
This is a practical review of the Go rewrite (not a formal audit).

Related: [architecture.md](./architecture.md), [install.md](./install.md),
[dev.md](./dev.md).

---

## Trust model (summary)

| Surface | Default exposure | Auth / isolation |
|---------|------------------|------------------|
| Control Unix socket | Local only (`/run/mount-wrapper/control.sock`) | Peer credentials: **root** or group **`mount-wrapper`** |
| HTTP API + SPA | Loopback `127.0.0.1:8787` | Optional Bearer `web_token` (empty = open on bind host) |
| Hooks (`hooks.d`) | Root-owned scripts under config path | Owner + not group/other-writable + realpath under hooks dir |
| FUSE mounts | Service user | External engine (`ratarmount-rs`); not reimplemented in Go |
| State DB | `/var/lib/mount-wrapper/state.db` | Single-writer `serve`; filesystem permissions |

mount-wrapper is an **orchestrator**: compromise of the service user implies
control of mounts, indexes, overlays, and hook execution under that user’s
privileges.

---

## Control plane (Unix socket)

**Path / mode:** `control_socket` (default `/run/mount-wrapper/control.sock`),
mode `0660`, best-effort chown to `mount-wrapper:mount-wrapper`.

**Auth:** `platform.PeerCredentials`

- Linux: `SO_PEERCRED` (pid, uid, gid)
- Darwin: `LOCAL_PEERCRED` / xucred (uid; **pid = -1**)
- Allow: uid **0** or membership in the configured service group

**Escape hatches (default off):**

| Mechanism | Name |
|-----------|------|
| Env | `MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH=1` |
| CLI | `serve --allow-unauth` |
| In-process | `service.Options.AllowAllAuth` |

When peercred is unavailable **and** the escape hatch is off, connections are
**denied**. Enable the hatch only for broken peercred environments (some
Darwin edges); do not set it in production Linux units without a documented
reason.

There is **no** `TARMOUNT_CONTROL_ALLOW_UNAUTH` (or any `TARMOUNT_*` dual
export). Upstream tarmount-wsl names are not re-exported.

**Protocol:** newline-delimited JSON (`v`/`op`). Unknown versions and bad JSON
fail closed with structured errors. Ops include destructive `purge` / `unmount`
/ `config_set` — treat socket access as full operator control.

---

## HTTP API + SPA

**Bind:** `web_host` / `web_port` (default `127.0.0.1:8787`). Non-loopback
binds log a warning (`web_host is not loopback…`) but are still allowed for
advanced setups.

**Token:** `web_token` defaults to **empty** → any client that can reach the
bind address may call `/api/*`. On loopback this matches “local operator”
intent; **set a token** if:

- you bind beyond loopback, or  
- untrusted local processes share the host, or  
- policy requires authenticated dashboards.

Non-empty token: `Authorization: Bearer <token>` or `?token=` on GET (SSE /
browser convenience).

**Doctor:** `mount-wrapper doctor` / `GET /api/doctor` includes check
`web_bind_security`: when `web_enabled` is true, `web_host` is non-loopback,
and `web_token` is empty → **severity warn** (does not hard-fail the report).
Loopback binds without a token are OK. Check **`control_socket_live`** dials the
configured control socket with a short `status` request: offline / dial fail /
`PERMISSION_DENIED` (not root or group `mount-wrapper`) → **warn**; reachable →
**info** with serve version when present. Doctor never hard-fails on this probe.
On Darwin, **`launchd_agent`** probes `launchctl` for the packaging Label
`com.hilather.mount-wrapper` (not loaded / missing tool → **warn** only).

**Destructive POST rate limits:** `purge`, `unmount` with `all: true`, and
`rescan` are limited per client IP (default **2s** min interval). Exceeding
returns HTTP **429** with `code: RATE_LIMITED`. Single-archive unmount / retry
are not rate-limited at this layer.

**Doctor / WSL info:** in-process; do not require the control socket but do
follow the same HTTP auth as other `/api/*` routes.

**Prometheus `GET /metrics`:** hand-written text exposition (not under
`/api/*`). Always present when the HTTP server is up (`web_enabled`).

| Bind | Auth |
|------|------|
| Loopback (`127.0.0.1`, `::1`, `localhost`) | **Open** — scrapers need no token (even if `web_token` is set for the dashboard) |
| Non-loopback | Same as `/api/*`: empty `web_token` → open; non-empty → Bearer / `?token=` |

Prefer loopback scrapes. If you bind the web UI beyond loopback, set
`web_token` so both the API and `/metrics` require authentication.

**Cardinality / scrape cost:** size and space-saved series are **fleet
aggregates** only (`mount_wrapper_*_size_bytes`, `mount_wrapper_space_saved_bytes`,
convert totals when present) — not one series per archive. Default scrapes
request status with `include_sizes` (metrics-op fallback), which can be slower
than count-only status when size providers hit disk/index. Operators who need
minimal scrape latency can build with `PrometheusOmitSizeGauges` (in-process
`api.ServerOptions`; not a YAML config key today).

---

## Hooks path escape & env

Hooks live under `hooks_dir` (typically `/etc/mount-wrapper/hooks.d`).

**Security policy (production defaults):**

- Hooks dir and scripts: not group- or world-writable
- Owner: root when running as root; otherwise root **or** the service UID
- **Realpath** of each executable must stay under realpath(`hooks_dir`) —
  blocks symlink escape outside the hooks tree
- `*.sample`, `*.disabled`, and README-like names are ignored

**Environment protocol:** hooks receive **`MOUNT_WRAPPER_*` only** for
mount-wrapper fields (archive id/path, mount path, index/overlay, config path,
hook name). Decision **D3**: no dual export of `TARMOUNT_*`. Pre-existing
`TARMOUNT_*` keys in the process environment are not stripped, but the daemon
does not set them for hooks.

**Note:** external engines may still read engine-specific env such as
`TARMOUNT_7Z_NONSOLID` / `RATARMOUNT_*` for convert/mount behavior; that is
engine protocol, not the hook env contract.

---

## Filesystem & packaging

- Service user/group: `mount-wrapper` / `mount-wrapper`
- systemd unit sketch: `NoNewPrivileges`, `ProtectSystem=strict`,
  `DeviceAllow=/dev/fuse`, long `TimeoutStopSec` for clean unmount
- State DB and data under `/var/lib/mount-wrapper/` should not be world-writable
- Do not run `serve` as root unless you intentionally accept root-owned mounts
  and broader FUSE capability (packaging prefers the service user)

---

## Operator checklist

1. Keep `web_host` on loopback unless you have network ACLs **and** a strong
   `web_token`. Run `mount-wrapper doctor` and fix `web_bind_security` if it warns.
2. Leave `MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH` unset (or `0`) in production.
3. Install hooks only as root-owned, non-writable scripts under `hooks.d`.
4. Restrict who is in group `mount-wrapper` (socket clients).
5. Treat the control socket and API as equivalent to shell access for mount
   lifecycle operations.
6. On macOS, keep `control_socket` short (doctor `control_socket_path_length`
   warns above ~100 bytes; see [macos.md](./macos.md)).

---

## Residual / not in scope

- No TLS termination for the local HTTP API (loopback-first design)
- No multi-tenant isolation between archives
- Rate limits are a flood brake, not a substitute for auth
- FUSE and engine binaries are trusted as installed on `PATH`
- Formal penetration test / CVE process not part of this document
