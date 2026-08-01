#!/usr/bin/env bash
# Seed /etc/mount-wrapper/config.yaml from the shipped example if missing.
# Never overwrites an existing operator config.
#
# Intended for package postinstall (via nfpm-postinstall.sh). Safe to re-run.
#
# Usage:
#   seed-config.sh
#   MW_ROOT=/tmp/fake-root seed-config.sh   # unit tests / prefix installs
#
# Env:
#   MW_ROOT  Optional filesystem prefix (default /). Paths become
#            $MW_ROOT/etc/mount-wrapper/config.yaml and
#            $MW_ROOT/usr/share/mount-wrapper/config.yaml.example.
#
# Exit: always 0 (best-effort; missing example or permission errors are non-fatal).

set -u

ROOT="${MW_ROOT:-/}"
if [[ "$ROOT" == "/" ]]; then
  DEST=/etc/mount-wrapper/config.yaml
  EXAMPLE=/usr/share/mount-wrapper/config.yaml.example
  ETC_DIR=/etc/mount-wrapper
else
  ROOT="${ROOT%/}"
  DEST="${ROOT}/etc/mount-wrapper/config.yaml"
  EXAMPLE="${ROOT}/usr/share/mount-wrapper/config.yaml.example"
  ETC_DIR="${ROOT}/etc/mount-wrapper"
fi

# Never overwrite existing operator config.
if [[ -e "$DEST" ]]; then
  exit 0
fi

if [[ ! -f "$EXAMPLE" ]]; then
  # Example not packaged / not present yet — skip silently.
  exit 0
fi

mkdir -p "$ETC_DIR" 2>/dev/null || true

if command -v install >/dev/null 2>&1; then
  install -m 0640 "$EXAMPLE" "$DEST" 2>/dev/null || {
    # Fallback without mode if install fails (e.g. odd platform).
    cp "$EXAMPLE" "$DEST" 2>/dev/null || exit 0
    chmod 0640 "$DEST" 2>/dev/null || true
  }
else
  cp "$EXAMPLE" "$DEST" 2>/dev/null || exit 0
  chmod 0640 "$DEST" 2>/dev/null || true
fi

# Best-effort ownership: root:mount-wrapper when the service group exists.
if command -v getent >/dev/null 2>&1 && getent group mount-wrapper >/dev/null 2>&1; then
  chown root:mount-wrapper "$DEST" 2>/dev/null || true
elif command -v chgrp >/dev/null 2>&1; then
  chgrp mount-wrapper "$DEST" 2>/dev/null || true
fi

exit 0
