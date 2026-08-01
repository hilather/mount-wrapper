#!/usr/bin/env bash
# nfpm/goreleaser postinstall: create service user/dirs if create-user.sh is present.
set -euo pipefail
if [[ -x /usr/share/mount-wrapper/create-user.sh ]]; then
  /usr/share/mount-wrapper/create-user.sh || true
elif [[ -x /usr/lib/mount-wrapper/create-user.sh ]]; then
  /usr/lib/mount-wrapper/create-user.sh || true
fi
# Reload systemd unit path if systemd is running.
if command -v systemctl >/dev/null 2>&1 && [[ -d /run/systemd/system ]]; then
  systemctl daemon-reload || true
fi
