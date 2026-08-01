# macOS support

**Status:** portable adapters + packaging examples; **GitHub Actions** runs
unit tests + binary smoke on `macos-latest` (no macFUSE).  
**Primary product target remains Ubuntu WSL2.** macOS is a best-effort
desktop port via **macFUSE + ratarmount-rs**.

### CI vs local

| Surface | CI (`ci.yml` → `macos-unit-smoke`) | Local / friend-test |
|---------|------------------------------------|---------------------|
| `go test ./...` | yes | yes |
| `make build` + `scripts/smoke-binary.sh` | yes (no FUSE) | yes |
| macFUSE install / real mount | **no** (not on default runners) | manual — this doc |
| launchd service | no | manual — packaging example |

---

## Prerequisites

1. **macOS** 12+ (Apple Silicon or Intel).  
2. **[macFUSE](https://macfuse.github.io/)** installed and allowed in
   System Settings → Privacy & Security (system extension prompts).  
3. **ratarmount-rs** (`mount_backend: rust` only)
   on `PATH`.  
4. Optional: **archiveconverter**, **p7zip** for convert paths.

Smoke engine alone:

```bash
mkdir -p /tmp/rm-mnt
ratarmount-rs -f some.tar.gz /tmp/rm-mnt
# Ctrl-C; then: umount /tmp/rm-mnt
```

---

## Config & paths

Use [`packaging/examples/config.yaml.macos.example`](../packaging/examples/config.yaml.macos.example):

| Key | Suggested layout |
|-----|------------------|
| state / mounts / indexes | `~/Library/Application Support/mount-wrapper/…` |
| `control_socket` / `pid_file` | `~/Library/Caches/mount-wrapper/run/…` |
| `web_enabled` | `true` (loopback `127.0.0.1:8787`) |
| `use_inotify` | `false` (poll primary) |
| `windows_visible` | `false` |
| Service user | **login user** (no system `mount-wrapper` user) |

### Socket path length

macOS `sockaddr_un` / `sun_path` is short (**~104 bytes**). Keep the control
socket under a short path such as:

```text
~/Library/Caches/mount-wrapper/run/control.sock
```

Avoid deep nested Application Support paths for the socket. If bind fails with
“filename too long” / invalid argument, shorten `control_socket`.

`mount-wrapper doctor` emits check **`windows_visible_parent_ox`** as macOS info
(keep `windows_visible: false`; parent o+x is WSL-oriented) and
**`control_socket_path_length`** on Darwin:
**warn** when `len(control_socket) > 100` (soft margin under the ~104 sun_path
limit). Severity is warn only (report still OK).

On Darwin, doctor also emits **`launchd_agent`**: best-effort `launchctl list`
(and `print` fallback) for Label **`com.hilather.mount-wrapper`** (packaging
example). **info** when loaded; **warn** when not loaded or `launchctl` is
missing — never hard-fails the report. Omitted on Linux.

Unit tests that bind real Unix sockets use
`internal/testutil.ShortUnixSocketPath` (short `/tmp` dir on Darwin) so
CI under long `/var/folders/...` temp paths stays green.

---

## launchd user agent

Example plist: [`packaging/launchd/com.hilather.mount-wrapper.plist.example`](../packaging/launchd/com.hilather.mount-wrapper.plist.example).

- Runs as the **logged-in user** (LaunchAgents), not as root.  
- Replace `REPLACE_HOME` with an absolute home path.  
- `serve` has **no** `--foreground` flag; launchd owns the job lifecycle.  
- Set `PATH` in the plist so Homebrew / cargo bins resolve.  
- Logs: `~/Library/Logs/mount-wrapper/`.  
- After load, `mount-wrapper doctor --json` should show **`launchd_agent`** as
  **info** (Label `com.hilather.mount-wrapper`).

```bash
cp packaging/launchd/com.hilather.mount-wrapper.plist.example \
  "$HOME/Library/LaunchAgents/com.hilather.mount-wrapper.plist"
# edit REPLACE_HOME and binary path
launchctl load "$HOME/Library/LaunchAgents/com.hilather.mount-wrapper.plist"
mount-wrapper doctor --json   # expect launchd_agent info when loaded
```

### Homebrew formula sketch

Formula example (not a published tap; **sketch is usable** after filling digests):
[`packaging/homebrew/mount-wrapper.rb.example`](../packaging/homebrew/mount-wrapper.rb.example).

Installs the **prebuilt** GitHub Release tarball (GoReleaser names, version **0.1.6**
placeholder in the sketch):

```text
mount-wrapper_#{version}_darwin_arm64.tar.gz   # Apple Silicon
mount-wrapper_#{version}_darwin_amd64.tar.gz   # Intel
# e.g. mount-wrapper_0.1.6_darwin_arm64.tar.gz
```

Fill `version` + both `sha256` lines from release `SHA256SUMS` (or snapshot):

```bash
# From a release/snapshot that produced dist/SHA256SUMS:
VERSION=0.1.6 SHA256SUMS=dist/SHA256SUMS \
  OUT=packaging/homebrew/mount-wrapper.rb \
  ./scripts/update-homebrew-formula.sh

brew install --formula ./packaging/homebrew/mount-wrapper.rb
```

Manual path: copy the `.example`, set `version` and replace
`REPLACE_ME_DARWIN_{ARM64,AMD64}`, then `brew install --formula …`.

**macFUSE** is a cask/system extension — allow it in System Settings; the formula
does not hard-require the cask. Engines stay external: **ratarmount-rs only**
(`mount_backend: rust`; Python ratarmount is not supported).

After install, seed config under Application Support (see formula `caveats`).
Keep `control_socket` short under Caches (`sun_path` ~104 bytes):

```bash
mkdir -p "$HOME/Library/Application Support/mount-wrapper"/{mounts,indexes,overlays,hooks.d}
mkdir -p "$HOME/Library/Caches/mount-wrapper/run" "$HOME/Library/Logs/mount-wrapper"
cp "$(brew --prefix)/share/mount-wrapper/config.yaml.macos.example" \
  "$HOME/Library/Application Support/mount-wrapper/config.yaml"
# control_socket: ~/Library/Caches/mount-wrapper/run/control.sock
```

Full install steps: [install.md](./install.md). Automated **tap** publish is still
residual ([parity.md](./parity.md)).

---

## Control socket auth

If CLI cannot talk to `serve` (“peer credentials unavailable”):

```bash
export MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH=1
mount-wrapper serve --config …   # and CLI in other terminals
```

Darwin peercred via `getpeereid` should work; requiring the escape hatch is a
bug to report. Default production posture: leave unauth **off**.

---

## Friend-test checklist

Record OS version, Apple Silicon vs Intel, macFUSE version; paste doctor JSON.

### A. Doctor

- [ ] `mount-wrapper doctor --config … --json` shows Darwin host  
- [ ] FUSE device / unmount tool OK after macFUSE install  
- [ ] systemd checks are informational (not WSL-required errors)  
- [ ] service-user messaging OK for login user  
- [ ] `ratarmount_bin` / backend resolution finds a binary  

### B. Manual unmount

- [ ] Mount a tiny `.tar.gz` with the engine in foreground  
- [ ] `umount <mountpoint>` works after killing the engine  

### C. Service smoke

- [ ] Drop a small archive into `source_dirs`  
- [ ] `serve` discovers → index/mount → mounted  
- [ ] Browse under `mount_root`  
- [ ] Unmount / stop cleans up without kernel panic  
- [ ] Remount after restart uses existing index  

### D. Control plane

- [ ] `status` works without `MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH`  
- [ ] If only unauth works → report peercred failure  

### E. Web

- [ ] With `web_enabled: true`, open `http://127.0.0.1:8787/`  

### F. Failures worth capturing

- macFUSE permission / “System Extension Blocked”  
- Hang on unmount  
- `ismount` never true after engine ready  
- Socket path too long  
- APFS case-sensitivity surprises  

---

## Non-goals (current)

- Notarized app bundle  
- Automatic macFUSE installer  
- FSEvents watcher (poll remains primary)  
- Windows UNC / DrvFs behavior on Darwin  
