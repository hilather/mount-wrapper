# Install & packaging

Operator-oriented install notes for **mount-wrapper**.

## Release matrix

Published GitHub Releases (tag `v*`, workflow `.github/workflows/release.yml`) use
**GoReleaser** with `CGO_ENABLED=0` (pure-Go SQLite). Same binary works on:

| Artifact | Platforms |
|----------|-----------|
| `mount-wrapper_*_linux_amd64.tar.gz` | Ubuntu x86_64, Rocky 8+ x86_64, WSL2 |
| `mount-wrapper_*_linux_arm64.tar.gz` | Ubuntu/Rocky aarch64 |
| `mount-wrapper_*_darwin_amd64.tar.gz` | macOS Intel |
| `mount-wrapper_*_darwin_arm64.tar.gz` | macOS Apple Silicon |
| `mount-wrapper_*.deb` | Ubuntu/Debian (amd64 + arm64) |
| `mount-wrapper_*.rpm` | Rocky/RHEL/Fedora (amd64 + arm64) |
| `mount-wrapper_*_linux_amd64_musl.tar.gz` | Optional D7 Alpine/musl-static (x86_64) |
| `mount-wrapper_*_linux_arm64_musl.tar.gz` | Optional D7 Alpine/musl-static (aarch64) |
| `SHA256SUMS` | Checksums for primary + musl archives |

Primary deb/rpm/tar stay pure-Go `CGO_ENABLED=0`. Optional `*_musl.tar.gz`
archives are built after GoReleaser (`scripts/build-musl.sh` +
`scripts/package-musl-release.sh`) and attached with `gh release upload` (not a
second goreleaser build id).

Local multi-arch snapshot (no publish):

```bash
make release-snapshot   # goreleaser only (CGO=0); needs goreleaser + node
make package-musl       # optional: Alpine build → dist/*_linux_*_musl.tar.gz
```

Binary smoke (no FUSE; works on Ubuntu host, Rocky 8 via container, and macOS):

```bash
make smoke
make smoke-package     # nfpm deb content inventory (soft-skip without nfpm/dpkg-deb)
# Always-on under make test: TestPackageTarInventory (synthetic tar + PACKAGE_TAR=)
# PACKAGE_TAR=dist/…_linux_amd64.tar.gz SKIP_DEB=1 ./scripts/smoke-package-contents.sh
make smoke-rocky        # docker/podman + rockylinux:8
make smoke-musl         # Alpine musl/static build + smoke (optional D7 path)
# On macOS (or anywhere with a built binary): ./scripts/smoke-binary.sh
```

GitHub Actions: Ubuntu + **macOS** unit/smoke in [`ci.yml`](../.github/workflows/ci.yml)
(`macos-unit-smoke` — no macFUSE); Rocky 8 + **`package-contents-smoke`** +
**`musl-static-smoke`** in [`smoke.yml`](../.github/workflows/smoke.yml). Real
FUSE is not default CI.

Field-test checklist: [field-test.md](./field-test.md). Changelog: [CHANGELOG.md](../CHANGELOG.md).
Cutting a release: [release.md](./release.md).

For local development see [dev.md](./dev.md). Architecture: [architecture.md](./architecture.md).
Python → Go cutover: [migration.md](./migration.md). Parity inventories & platform checklists: [parity.md](./parity.md).
Security review: [security.md](./security.md). Man page: `packaging/man/mount-wrapper.1`.

---

## Paths (Linux FHS)

| Path | Role |
|------|------|
| `/usr/bin/mount-wrapper` | CLI + daemon binary |
| `/etc/mount-wrapper/config.yaml` | Main config |
| `/etc/mount-wrapper/hooks.d/` | First-mount hooks |
| `/etc/mount-wrapper/env` | Optional systemd `EnvironmentFile` (may be absent) |
| `/var/lib/mount-wrapper/` | mounts, indexes, overlays, state.db, inbox, converted |
| `/var/log/mount-wrapper/` | Optional service logs directory |
| `/run/mount-wrapper/` | `control.sock` + pidfile (`RuntimeDirectory`) |
| `/lib/systemd/system/mount-wrapper.service` | systemd unit |

**Service user/group:** `mount-wrapper` / `mount-wrapper` (decision D9).

**Control socket:** mode `0660`, best-effort chown to `mount-wrapper:mount-wrapper`.
Auth: root **or** group `mount-wrapper` (peercred). Escape hatch:
`MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH=1` (default off).

**Web UI:** embedded in `serve` when `web_enabled: true` (default bind
`127.0.0.1:8787`). No separate web systemd unit.

---

## Install engines (not bundled)

mount-wrapper **orchestrates** external tools; it does not vendor a private
Python ratarmount venv.

| Tool | Role | Notes |
|------|------|--------|
| **ratarmount-rs** | Default mount engine (`mount_backend: rust`) | On `PATH` as `ratarmount-rs` |
| **fuse3** | Linux FUSE userland (`fusermount3`) | `/dev/fuse` present |
| **macFUSE** | macOS FUSE | System Settings approval |
| **archiveconverter** | Optional solid→non-solid 7z convert | Often `~/.local/bin/archiveconverter` |
| **7z** / **p7zip** | Optional flatten / zip repack helpers | `7z` or `7za` on `PATH` |

### Ubuntu / Debian

```bash
sudo apt update
sudo apt install fuse3 p7zip-full
# Install ratarmount-rs from its upstream release (cargo or binary).
# Optional: archiveconverter from its project; ensure PATH for service user.
```

### Rocky 8 / RHEL-like

```bash
sudo dnf install fuse3 p7zip p7zip-plugins
# ratarmount-rs: use a CGO_ENABLED=0 / static-friendly release binary (see below).
```

**glibc note:** Rocky 8 ships **glibc 2.28**. Binaries built on Ubuntu 22.04+
with dynamic glibc may fail with “GLIBC_2.xx not found”. Prefer:

1. **Primary (release default):** `CGO_ENABLED=0` pure-Go builds (SQLite is
   `modernc.org/sqlite`). GoReleaser and `make build` use this path; the
   binary is already **statically linked** and runs on Rocky 8 / Ubuntu / WSL2.
2. **Optional (D7 musl/static path):** build inside Alpine for an explicit
   static artifact, CI smoke, and **published** `*_musl.tar.gz` on GitHub
   Releases — does **not** replace the primary goreleaser matrix or deb/rpm:

```bash
make build-musl
# → bin/mount-wrapper-linux-amd64-musl (+ bin/mount-wrapper-musl for host arch)
# Multi-arch + release tarballs into dist/:
make package-musl
# → dist/mount-wrapper_${VERSION}_linux_amd64_musl.tar.gz
# → dist/mount-wrapper_${VERSION}_linux_arm64_musl.tar.gz
file bin/mount-wrapper-linux-amd64-musl   # expect "statically linked"
BIN=./bin/mount-wrapper-musl ./scripts/smoke-binary.sh
# or: make smoke-musl
```

Scripts: [`scripts/build-musl.sh`](../scripts/build-musl.sh),
[`scripts/package-musl-release.sh`](../scripts/package-musl-release.sh)
(docker/podman + `golang:1.25-alpine`). CI: `smoke.yml` → **`musl-static-smoke`**;
`release.yml` packages and `gh release upload`s musl tarballs after GoReleaser.

**fuse3:** enable the FUSE kernel module if needed (`modprobe fuse`); ensure
`/dev/fuse` is present. Doctor reports FUSE availability.

### macOS

1. Install [macFUSE](https://macfuse.github.io/) and allow the system extension.  
2. Install `ratarmount-rs` on `PATH`.  
3. Optional: archiveconverter / p7zip via brew.  

See [macos.md](./macos.md).

---

## Linux: quick install (from source / binary)

```bash
# 1) Binary
make build
sudo install -m 0755 bin/mount-wrapper /usr/bin/mount-wrapper

# 2) User, group, dirs, optional user_allow_other
sudo packaging/scripts/create-user.sh

# 3) Config + unit
sudo cp packaging/examples/config.yaml.example /etc/mount-wrapper/config.yaml
# edit source_dirs — service user must read them
sudo install -m 0644 packaging/systemd/mount-wrapper.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now mount-wrapper

# 4) Doctor + source path drop-in
sudo -u mount-wrapper mount-wrapper doctor --config /etc/mount-wrapper/config.yaml
# Optional: preview drop-in without writing
mount-wrapper doctor --config /etc/mount-wrapper/config.yaml --fix-systemd --dry-run
sudo mount-wrapper doctor --config /etc/mount-wrapper/config.yaml --fix-systemd
sudo systemctl daemon-reload && sudo systemctl restart mount-wrapper
```

Optional env file: copy `packaging/env.example` → `/etc/mount-wrapper/env`
(e.g. extend `PATH` for engines installed under a home directory).

### systemd notes

| Setting | Why |
|---------|-----|
| `TimeoutStopSec=300` | Stop unmounts the FUSE pool; 90s default is often too short |
| `DeviceAllow=/dev/fuse rwm` | Required for FUSE mounts under hardened device policy |
| `ProtectSystem=strict` + `ReadWritePaths=` | Harden FS; state under `/var/lib/mount-wrapper` stays writable |
| `EnvironmentFile=-/etc/mount-wrapper/env` | Optional overrides; `-` = file may be missing |
| `User=` / `Group=mount-wrapper` | D9 service identity |

Extend `ReadOnlyPaths` / `ReadWritePaths` via `doctor --fix-systemd` (writes
`/etc/systemd/system/mount-wrapper.service.d/sources.conf`):

- **`source_dirs`** → `ReadOnlyPaths` (plus `/mnt/<letter>` for DrvFs drive roots)
- **`archives_dir`** and custom absolute **data roots** → `ReadWritePaths` when not
  already covered by the packaged bases (`/var/lib/mount-wrapper`,
  `/var/log/mount-wrapper`, `/run/mount-wrapper`):
  `mount_root`, `index_dir`, `overlay_dir`, `convert_7z_cache_dir`,
  `archiveconverter_output_dir`

Defaults under `/var/lib/mount-wrapper/…` are not re-listed. Paths are deduped.

**Preview without writing:** `mount-wrapper doctor --config … --fix-systemd --dry-run`
prints the generated unit in report **notes** (text) / check **details.content**
(JSON) and does not mkdir/write the drop-in. Then apply for real with
`--fix-systemd` (no `--dry-run`), and: `systemctl daemon-reload && systemctl restart mount-wrapper`.

---

## WSL2

1. Merge `packaging/wsl.conf.snippet` into `/etc/wsl.conf` (`systemd=true`, automount options).  
2. From Windows: `wsl --shutdown`, then reopen the distro.  
3. Install as Linux above; use DrvFs paths or `D:\…` style `source_dirs`.  
4. For UNC visibility (`\\wsl.localhost\…`), keep `windows_visible: true` and
   `user_allow_other` in `/etc/fuse.conf` (`create-user.sh` enables when possible).
   Parents of `mount_root` must be other-executable (`o+x`); `create-user.sh`
   sets this on `/var/lib/mount-wrapper` and `…/mounts` — custom roots need
   operator chmod (see [architecture.md](./architecture.md)). Run
   `mount-wrapper doctor` and fix **`windows_visible_parent_ox`** if it warns
   (message includes `chmod o+x …` for paths lacking other-execute).
5. **Task Scheduler (optional):** import
   `packaging/windows-task-scheduler.xml.example` so login runs
   `wsl.exe -d <Distro> --exec /bin/true` and systemd can start the unit
   without an interactive terminal.

---

## macOS: user agent

```bash
make build
# install binary somewhere on PATH, e.g. /usr/local/bin or ~/bin

mkdir -p "$HOME/Library/Application Support/mount-wrapper"/{mounts,indexes,overlays,hooks.d}
mkdir -p "$HOME/Library/Caches/mount-wrapper/run" "$HOME/Library/Logs/mount-wrapper"
cp packaging/examples/config.yaml.macos.example \
  "$HOME/Library/Application Support/mount-wrapper/config.yaml"
# replace YOU; keep control_socket under Caches (path length!)

cp packaging/launchd/com.hilather.mount-wrapper.plist.example \
  "$HOME/Library/LaunchAgents/com.hilather.mount-wrapper.plist"
# replace REPLACE_HOME with $HOME (absolute)

launchctl load "$HOME/Library/LaunchAgents/com.hilather.mount-wrapper.plist"
mount-wrapper doctor --config "$HOME/Library/Application Support/mount-wrapper/config.yaml"
```

**Socket path length:** macOS `sockaddr_un` is short (~104 bytes). Prefer
`~/Library/Caches/mount-wrapper/run/control.sock`. Deep Application Support
paths can fail `bind`.

### Homebrew formula sketch

Example formula: [`packaging/homebrew/mount-wrapper.rb.example`](../packaging/homebrew/mount-wrapper.rb.example)
(not a published tap; **local `brew install --formula` is supported** after digests
are filled). Prefer **release tarballs** (GoReleaser):

```text
https://github.com/hilather/mount-wrapper/releases/download/v0.1.4/mount-wrapper_0.1.4_darwin_arm64.tar.gz
https://github.com/hilather/mount-wrapper/releases/download/v0.1.4/mount-wrapper_0.1.4_darwin_amd64.tar.gz
```

(`arm64` = Apple Silicon, `amd64` = Intel; version in the sketch tracks **0.1.4**
until the next release bump.)

Fill both platform `sha256` values from `SHA256SUMS` with the helper script:

```bash
# After make release-snapshot (or download SHA256SUMS from a GitHub Release):
VERSION=0.1.4 SHA256SUMS=dist/SHA256SUMS \
  OUT=packaging/homebrew/mount-wrapper.rb \
  ./scripts/update-homebrew-formula.sh

brew install --formula ./packaging/homebrew/mount-wrapper.rb
brew info mount-wrapper   # caveats: macFUSE + Application Support + short control socket
```

Or copy the example and replace `REPLACE_ME_DARWIN_ARM64` /
`REPLACE_ME_DARWIN_AMD64` by hand. CI does **not** run `brew install`.

Formula `caveats` cover macFUSE System Settings approval, **ratarmount-rs only**
(no Python ratarmount), config under `~/Library/Application Support/mount-wrapper/`,
and `control_socket` under `~/Library/Caches/mount-wrapper/run/` (path length).

Residual: automated **tap** publish (`brew install hilather/tap/mount-wrapper`).
The formula sketch itself is ship-ready for manual formula install.

See [macos.md](./macos.md) and [release.md](./release.md).

---

## Release tooling

### Version ldflags (Makefile)

```make
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
```

`make build` embeds these into `main.version` / `main.commit` / `main.date`
(see `cmd/mount-wrapper/main.go`). `mount-wrapper version` prints them.

### goreleaser

[`.goreleaser.yaml`](../.goreleaser.yaml) — linux/darwin × amd64/arm64,
`CGO_ENABLED=0` (**primary** release default), checksum file **`SHA256SUMS`**.
Tag publish: [`.github/workflows/release.yml`](../.github/workflows/release.yml).

Musl/static is **not** a second goreleaser build id (avoids a fragile dual
matrix). The release workflow builds Alpine binaries after GoReleaser and
attaches optional archives with `gh release upload`.

```bash
make release-snapshot    # or: goreleaser release --snapshot --clean
# dist/*.tar.gz + dist/SHA256SUMS  (CGO=0 only; no docker required)
sha256sum -c dist/SHA256SUMS

# Optional local musl packages (needs docker/podman):
make package-musl
# dist/mount-wrapper_*_linux_*_musl.tar.gz + updated SHA256SUMS
```

### Musl / Alpine static (optional D7)

| Step | Command / artifact |
|------|--------------------|
| Local binary | `make build-musl` → `bin/mount-wrapper-linux-{amd64,arm64}-musl` |
| Local tarball | `make package-musl` → `dist/mount-wrapper_${VERSION}_linux_{amd64,arm64}_musl.tar.gz` |
| Smoke | `make smoke-musl` |
| CI smoke | `smoke.yml` → `musl-static-smoke` |
| Release attach | `release.yml` after GoReleaser → `gh release upload` + refreshed `SHA256SUMS` |

```bash
make build-musl          # scripts/build-musl.sh → bin/*-musl
make package-musl        # build + package into dist/*_musl.tar.gz
make smoke-musl          # build + smoke-binary with BIN=./bin/mount-wrapper-musl
```

Needs **docker** or **podman**. Override image with `GO_IMAGE=golang:1.25-alpine`.
Cross-compile arm64 without QEMU: `ARCHS=arm64 ./scripts/build-musl.sh`
(run smoke only for host arch). Tarball layout: `mount-wrapper` binary +
`LICENSE` / `README.md` / `install.md` / `MUSL.txt`.

### nfpm (deb/rpm)

Tag releases build `.deb` / `.rpm` via **GoReleaser** nfpms (`.goreleaser.yaml`)
with `scripts.postinstall` → `packaging/scripts/nfpm-postinstall.sh` (runs
`create-user.sh`, first-install `seed-config.sh`, then `systemctl daemon-reload`
when present).

Standalone local packages use the same postinstall:

```bash
make build
VERSION=$(git describe --tags --always) nfpm package -f packaging/nfpm.yaml -p deb
```

**Content inventory smoke** (no root install; asserts package members):

```bash
# Deb path — needs nfpm + dpkg-deb. Soft-skips (exit 0) if either is missing.
make smoke-package
# CI / hard fail when tools should be present:
REQUIRE_TOOLS=1 ./scripts/smoke-package-contents.sh --build

# Tar path — no nfpm required. Inventories GoReleaser-relative members:
PACKAGE_TAR=dist/mount-wrapper_*_linux_amd64.tar.gz SKIP_DEB=1 \
  ./scripts/smoke-package-contents.sh
# Or after snapshot: CHECK_TAR=1 ./scripts/smoke-package-contents.sh
# Always-on under make test: TestPackageTarInventory (synthetic tar.gz + PACKAGE_TAR=)
```

Required deb members checked by
[`scripts/smoke-package-contents.sh`](../scripts/smoke-package-contents.sh):

| Path | Role |
|------|------|
| `/usr/bin/mount-wrapper` | binary |
| `/lib/systemd/system/mount-wrapper.service` | unit |
| `/usr/share/mount-wrapper/config.yaml.example` | config example |
| `/usr/share/mount-wrapper/seed-config.sh` | first-install seed |
| `/usr/share/mount-wrapper/create-user.sh` | service user/dirs |
| `/usr/share/man/man1/mount-wrapper.1` | man page |

Required **tar** members (`REQUIRED_TAR_MEMBERS`, GoReleaser
`archives.files` relative layout — not FHS):

| Member | Role |
|--------|------|
| `mount-wrapper` | binary |
| `packaging/systemd/mount-wrapper.service` | unit |
| `packaging/examples/config.yaml.example` | config example |
| `packaging/scripts/seed-config.sh` | first-install seed |
| `packaging/scripts/create-user.sh` | service user/dirs |
| `packaging/man/mount-wrapper.1` | man page |

`PACKAGE_TAR=` alone runs tar checks without nfpm (`SKIP_DEB=1` / `--tar-only`
skips deb even when tools are present). CI job **package-contents-smoke** in
[`smoke.yml`](../.github/workflows/smoke.yml) installs nfpm and runs the script
with `REQUIRE_TOOLS=1` for the deb path; unit tests always cover tar members via
`TestPackageTarInventory`.

[`packaging/nfpm.yaml`](../packaging/nfpm.yaml) ships the binary, unit, man page,
examples (including `config.debug.yaml.example`), `LICENSE`, `create-user.sh`, and
`seed-config.sh` (aligned with `.goreleaser.yaml` nfpms contents). On **first
install**, postinstall seeds `/etc/mount-wrapper/config.yaml` from
`/usr/share/mount-wrapper/config.yaml.example` when the dest is missing
(`packaging/scripts/seed-config.sh`, mode `0640`, best-effort `root:mount-wrapper`).
**Never overwrites** an existing operator config on upgrade or reinstall.
Edit `source_dirs` (and other keys) after install before enabling the unit.

### SHA256SUMS

Publish `SHA256SUMS` next to release archives. Operators should verify before
installing:

```bash
sha256sum -c SHA256SUMS
```

---

## Residual (not done in this polish)

- Automated Homebrew **tap** publish (formula sketch + `scripts/update-homebrew-formula.sh` are usable locally; deb/rpm + musl tarballs already publish on `v*` via `release.yml`)  
- macOS CI with **macFUSE** (default CI is unit + binary smoke only; see [dev.md](./dev.md))  
- Notarized macOS app / automatic macFUSE install  
- Operator-facing package-contents Makefile target is still **deb-first** (`make smoke-package`); release tar under `dist/` still needs a fresh `release-snapshot` for `CHECK_TAR=1` (stale archives may predate new files). Synthetic tar inventory is always covered by `TestPackageTarInventory`

---

## Packaging tree

```text
packaging/
  systemd/mount-wrapper.service
  launchd/com.hilather.mount-wrapper.plist.example
  homebrew/mount-wrapper.rb.example   # local brew --formula after SHA fill-in
  examples/config.yaml.example
  examples/config.yaml.macos.example
  examples/config.debug.yaml.example
  examples/hooks.d/*.sample
  scripts/create-user.sh
  scripts/seed-config.sh      # first-install config seed (MW_ROOT= for tests)
  scripts/nfpm-postinstall.sh
  env.example
  wsl.conf.snippet
  windows-task-scheduler.xml.example
  nfpm.yaml
scripts/update-homebrew-formula.sh    # VERSION + SHA256SUMS → formula digests
.goreleaser.yaml
```
