#!/usr/bin/env bash
# Smoke the linux/amd64 CGO=0 binary inside Rocky Linux 8 (glibc 2.28 era).
#
# Usage (from repo root, needs docker or podman):
#   ./scripts/smoke-rocky8.sh
#   ./scripts/smoke-rocky8.sh --build   # make build first
#
# Does not require FUSE or ratarmount inside the container.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

RUNTIME=""
if command -v docker >/dev/null 2>&1; then
  RUNTIME=docker
elif command -v podman >/dev/null 2>&1; then
  RUNTIME=podman
else
  echo "smoke-rocky8: need docker or podman" >&2
  exit 1
fi

DO_BUILD=0
for arg in "$@"; do
  case "$arg" in
    --build) DO_BUILD=1 ;;
  esac
done

if [[ "$DO_BUILD" -eq 1 ]] || [[ ! -x ./bin/mount-wrapper ]]; then
  export PATH="${HOME}/.local/go/bin:${PATH:-}"
  export CGO_ENABLED=0
  make build
fi

if [[ ! -x ./bin/mount-wrapper ]]; then
  echo "smoke-rocky8: ./bin/mount-wrapper missing" >&2
  exit 1
fi

# Confirm host binary is ELF for linux (optional soft check).
if command -v file >/dev/null 2>&1; then
  file ./bin/mount-wrapper || true
fi

echo "==> rocky:8 container smoke"
$RUNTIME run --rm \
  -v "$ROOT/bin/mount-wrapper:/usr/local/bin/mount-wrapper:ro" \
  -v "$ROOT/scripts/smoke-binary.sh:/smoke-binary.sh:ro" \
  rockylinux:8 \
  bash -lc '
    set -euo pipefail
    # Minimal tools for smoke-binary (mktemp, tar not required for --once empty)
    yum -y -q install which 2>/dev/null || true
    BIN=/usr/local/bin/mount-wrapper bash /smoke-binary.sh
  '

echo "==> smoke-rocky8 OK"
