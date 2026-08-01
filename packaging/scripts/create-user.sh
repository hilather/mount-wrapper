#!/usr/bin/env bash
# Create mount-wrapper system user/group and FHS directories.
# Intended for operators / package postinst — not invoked by unit tests.
#
# Usage:
#   sudo packaging/scripts/create-user.sh
#   sudo packaging/scripts/create-user.sh --no-fuse-conf
#
# Safe to re-run (idempotent). Does not install the binary or enable systemd.

set -euo pipefail

USER_NAME=mount-wrapper
GROUP_NAME=mount-wrapper
HOME_DIR=/var/lib/mount-wrapper
ENABLE_FUSE_CONF=1

usage() {
  cat <<'EOF'
Usage: create-user.sh [--no-fuse-conf] [--help]

Creates system group/user "mount-wrapper", state/runtime/config dirs, and
optionally enables user_allow_other in /etc/fuse.conf (for windows_visible /
allow_other mounts under WSL).

Requires root. Not used by the test suite.
EOF
}

for arg in "$@"; do
  case "$arg" in
    --no-fuse-conf) ENABLE_FUSE_CONF=0 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ "$(id -u)" -ne 0 ]]; then
  echo "create-user.sh must run as root (e.g. sudo $0)" >&2
  exit 1
fi

if ! getent group "$GROUP_NAME" >/dev/null 2>&1; then
  if command -v groupadd >/dev/null 2>&1; then
    groupadd --system "$GROUP_NAME"
  else
    echo "groupadd not found; create group $GROUP_NAME manually" >&2
    exit 1
  fi
fi

if ! getent passwd "$USER_NAME" >/dev/null 2>&1; then
  if command -v useradd >/dev/null 2>&1; then
    useradd --system --gid "$GROUP_NAME" --home-dir "$HOME_DIR" \
      --shell /usr/sbin/nologin --comment "mount-wrapper service" "$USER_NAME"
  else
    echo "useradd not found; create user $USER_NAME manually" >&2
    exit 1
  fi
fi

install -d -o root -g root -m 0755 /etc/mount-wrapper
install -d -o root -g "$GROUP_NAME" -m 0750 /etc/mount-wrapper/hooks.d
install -d -o "$USER_NAME" -g "$GROUP_NAME" -m 0750 \
  /var/lib/mount-wrapper \
  /var/lib/mount-wrapper/inbox \
  /var/lib/mount-wrapper/mounts \
  /var/lib/mount-wrapper/indexes \
  /var/lib/mount-wrapper/overlays \
  /var/lib/mount-wrapper/converted \
  /var/log/mount-wrapper
# Runtime dir is normally created by systemd RuntimeDirectory=; seed for manual runs.
install -d -o "$USER_NAME" -g "$GROUP_NAME" -m 0750 /run/mount-wrapper

# Parent execute bit helps \\wsl.localhost\ traversal of mount trees when
# windows_visible mounts use allow_other.
chmod o+x /var/lib/mount-wrapper /var/lib/mount-wrapper/mounts 2>/dev/null || true

if [[ "$ENABLE_FUSE_CONF" -eq 1 && -w /etc/fuse.conf ]]; then
  if grep -Eq '^[[:space:]]*user_allow_other[[:space:]]*$' /etc/fuse.conf 2>/dev/null; then
    :
  elif grep -Eq '^[[:space:]]*#[[:space:]]*user_allow_other' /etc/fuse.conf 2>/dev/null; then
    # Uncomment first commented directive.
    sed -i 's/^[[:space:]]*#[[:space:]]*user_allow_other.*/user_allow_other/' /etc/fuse.conf
  else
    printf '\n# Enabled by mount-wrapper create-user.sh (windows_visible / allow_other)\nuser_allow_other\n' \
      >>/etc/fuse.conf
  fi
elif [[ "$ENABLE_FUSE_CONF" -eq 1 ]]; then
  echo "note: could not update /etc/fuse.conf (missing or not writable); set user_allow_other manually if needed" >&2
fi

echo "ok: user/group $USER_NAME:$GROUP_NAME and directories ready"
echo "next: ensure /etc/mount-wrapper/config.yaml (package postinstall seeds from the example if missing), then enable mount-wrapper.service"
