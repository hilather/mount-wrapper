#!/usr/bin/env bash
# Binary / install smoke for mount-wrapper (no FUSE required).
#
# Usage:
#   ./scripts/smoke-binary.sh                 # uses ./bin/mount-wrapper or PATH
#   BIN=/path/to/mount-wrapper ./scripts/smoke-binary.sh
#   ./scripts/smoke-binary.sh --build         # make build first
#
# Exit 0 on success. Safe for CI (Ubuntu, Rocky 8 container, macOS runners).
# Does not need fuse3 / macFUSE — version, doctor, config show, serve --once only.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

DO_BUILD=0
for arg in "$@"; do
  case "$arg" in
    --build) DO_BUILD=1 ;;
    -h|--help)
      sed -n '2,12p' "$0"
      exit 0
      ;;
  esac
done

if [[ "$DO_BUILD" -eq 1 ]]; then
  export PATH="${HOME}/.local/go/bin:${PATH:-}"
  make build
fi

BIN="${BIN:-}"
if [[ -z "$BIN" ]]; then
  if [[ -x ./bin/mount-wrapper ]]; then
    BIN=./bin/mount-wrapper
  elif command -v mount-wrapper >/dev/null 2>&1; then
    BIN="$(command -v mount-wrapper)"
  else
    echo "smoke-binary: mount-wrapper not found (set BIN= or --build)" >&2
    exit 1
  fi
fi

echo "==> binary: $BIN"
"$BIN" version
"$BIN" help >/dev/null

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

cat >"$TMP/config.yaml" <<EOF
version: 1
source_dirs:
  - $TMP/inbox
mount_root: $TMP/mounts
index_dir: $TMP/indexes
overlay_dir: $TMP/overlays
state_db: $TMP/state.db
hooks_dir: $TMP/hooks.d
control_socket: $TMP/control.sock
pid_file: $TMP/mount-wrapper.pid
poll_interval_seconds: 3600
reconcile_interval_seconds: 3600
use_inotify: false
write_overlay: false
web_enabled: false
EOF
mkdir -p "$TMP/inbox" "$TMP/hooks.d"

echo "==> doctor --json (offline)"
"$BIN" doctor --config "$TMP/config.yaml" --json >/dev/null

echo "==> config show --local"
"$BIN" config show --local --config "$TMP/config.yaml" >/dev/null

echo "==> serve --once"
"$BIN" serve --config "$TMP/config.yaml" --once --allow-unauth

echo "==> smoke-binary OK"
