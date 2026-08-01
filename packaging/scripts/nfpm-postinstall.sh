#!/usr/bin/env bash
# nfpm/goreleaser postinstall: create service user/dirs, seed default config if
# missing, then reload systemd. Best-effort throughout (never fail the package).
#
# Config seed never overwrites an existing /etc/mount-wrapper/config.yaml.
# Logic lives in seed-config.sh (shipped under /usr/share/mount-wrapper/; also
# callable with MW_ROOT= for tests without root).
set -euo pipefail

if [[ -x /usr/share/mount-wrapper/create-user.sh ]]; then
  /usr/share/mount-wrapper/create-user.sh || true
elif [[ -x /usr/lib/mount-wrapper/create-user.sh ]]; then
  /usr/lib/mount-wrapper/create-user.sh || true
fi

# First-install only: copy example → /etc/mount-wrapper/config.yaml if absent.
if [[ -x /usr/share/mount-wrapper/seed-config.sh ]]; then
  /usr/share/mount-wrapper/seed-config.sh || true
elif [[ -f /usr/share/mount-wrapper/seed-config.sh ]]; then
  bash /usr/share/mount-wrapper/seed-config.sh || true
fi

# Reload systemd unit path if systemd is running.
if command -v systemctl >/dev/null 2>&1 && [[ -d /run/systemd/system ]]; then
  systemctl daemon-reload || true
fi
