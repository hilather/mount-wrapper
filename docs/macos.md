# macOS support

**Status:** portable adapters + packaging examples landable on Linux CI.  
**Primary product target remains Ubuntu WSL2.** macOS is a best-effort
desktop port via **macFUSE + ratarmount(-rs)**.

---

## Prerequisites

1. **macOS** 12+ (Apple Silicon or Intel).  
2. **[macFUSE](https://macfuse.github.io/)** installed and allowed in
   System Settings → Privacy & Security (system extension prompts).  
3. **ratarmount-rs** (preferred, `mount_backend: rust`) or Python **ratarmount**
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

---

## launchd user agent

Example plist: [`packaging/launchd/com.hilather.mount-wrapper.plist.example`](../packaging/launchd/com.hilather.mount-wrapper.plist.example).

- Runs as the **logged-in user** (LaunchAgents), not as root.  
- Replace `REPLACE_HOME` with an absolute home path.  
- `serve` has **no** `--foreground` flag; launchd owns the job lifecycle.  
- Set `PATH` in the plist so Homebrew / cargo bins resolve.  
- Logs: `~/Library/Logs/mount-wrapper/`.

```bash
cp packaging/launchd/com.hilather.mount-wrapper.plist.example \
  "$HOME/Library/LaunchAgents/com.hilather.mount-wrapper.plist"
# edit REPLACE_HOME and binary path
launchctl load "$HOME/Library/LaunchAgents/com.hilather.mount-wrapper.plist"
```

Homebrew formula sketch: [`packaging/homebrew/mount-wrapper.rb.example`](../packaging/homebrew/mount-wrapper.rb.example).

Full install steps: [install.md](./install.md).

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
