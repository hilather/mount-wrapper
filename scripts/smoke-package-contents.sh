#!/usr/bin/env bash
# Package content inventory smoke for mount-wrapper.
#
# Builds a .deb via packaging/nfpm.yaml (default CI path) and asserts required
# install paths with dpkg-deb -c. Optionally inventories a release tar.gz.
#
# Usage:
#   ./scripts/smoke-package-contents.sh
#   ./scripts/smoke-package-contents.sh --build
#   REQUIRE_TOOLS=1 ./scripts/smoke-package-contents.sh   # fail if tools missing
#   PACKAGE_TAR=dist/mount-wrapper_*_linux_amd64.tar.gz ./scripts/smoke-package-contents.sh
#   CHECK_TAR=1 ./scripts/smoke-package-contents.sh       # auto-pick dist/*_linux_amd64.tar.gz
#
# Skip policy (local / unit-test friendly):
#   When nfpm or dpkg-deb is missing and REQUIRE_TOOLS is unset/0, print
#   "SKIP: …" and exit 0. Set REQUIRE_TOOLS=1 after installing tools in CI.
#
# Required deb paths (must appear in dpkg-deb -c):
#   ./usr/bin/mount-wrapper
#   ./lib/systemd/system/mount-wrapper.service
#   ./usr/share/mount-wrapper/config.yaml.example
#   ./usr/share/mount-wrapper/seed-config.sh
#   ./usr/share/mount-wrapper/create-user.sh
#   ./usr/share/man/man1/mount-wrapper.1
#
# Exit 0 on success or soft skip; non-zero on hard failure.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export PATH="${HOME}/.local/go/bin:${HOME}/go/bin:${PATH:-}"

DO_BUILD=0
REQUIRE_TOOLS="${REQUIRE_TOOLS:-0}"
CHECK_TAR="${CHECK_TAR:-0}"
PACKAGE_TAR="${PACKAGE_TAR:-}"
OUT_DIR="${OUT_DIR:-}"
KEEP_OUT="${KEEP_OUT:-0}"

for arg in "$@"; do
  case "$arg" in
    --build) DO_BUILD=1 ;;
    --require-tools) REQUIRE_TOOLS=1 ;;
    --check-tar) CHECK_TAR=1 ;;
    -h|--help)
      sed -n '2,30p' "$0"
      exit 0
      ;;
    *)
      echo "smoke-package-contents: unknown arg: $arg" >&2
      exit 2
      ;;
  esac
done

skip_or_fail() {
  local msg="$1"
  if [[ "$REQUIRE_TOOLS" == "1" ]]; then
    echo "smoke-package-contents: ERROR: $msg (REQUIRE_TOOLS=1)" >&2
    exit 1
  fi
  echo "SKIP: smoke-package-contents: $msg"
  exit 0
}

if ! command -v nfpm >/dev/null 2>&1; then
  skip_or_fail "nfpm not found (install: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest)"
fi
if ! command -v dpkg-deb >/dev/null 2>&1; then
  skip_or_fail "dpkg-deb not found (Debian/Ubuntu package dpkg-dev or dpkg)"
fi

if [[ "$DO_BUILD" -eq 1 ]] || [[ ! -x ./bin/mount-wrapper ]]; then
  echo "==> make build"
  make build
fi
if [[ ! -x ./bin/mount-wrapper ]]; then
  echo "smoke-package-contents: ./bin/mount-wrapper missing after build" >&2
  exit 1
fi

# Required install paths as listed by dpkg-deb -c (leading ./).
REQUIRED_DEB_PATHS=(
  "./usr/bin/mount-wrapper"
  "./lib/systemd/system/mount-wrapper.service"
  "./usr/share/mount-wrapper/config.yaml.example"
  "./usr/share/mount-wrapper/seed-config.sh"
  "./usr/share/mount-wrapper/create-user.sh"
  "./usr/share/man/man1/mount-wrapper.1"
)

# Extra paths we ship and want to keep in inventory (non-fatal if policy softens).
EXTRA_DEB_PATHS=(
  "./usr/share/mount-wrapper/config.debug.yaml.example"
  "./usr/share/mount-wrapper/hooks.d/10-list-tree.sh.sample"
  "./usr/share/mount-wrapper/env.example"
  "./usr/share/doc/mount-wrapper/install.md"
  "./usr/share/doc/mount-wrapper/LICENSE"
)

if [[ -z "$OUT_DIR" ]]; then
  OUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mw-pkg-contents.XXXXXX")"
  if [[ "$KEEP_OUT" != "1" ]]; then
    trap 'rm -rf "$OUT_DIR"' EXIT
  else
    echo "==> keeping OUT_DIR=$OUT_DIR"
  fi
else
  mkdir -p "$OUT_DIR"
fi

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}"
# nfpm rejects some git-describe chars in versions; sanitize lightly.
VERSION_SAFE="$(printf '%s' "$VERSION" | sed 's/^v//' | tr -c 'A-Za-z0-9.+~-' '-')"
export VERSION="$VERSION_SAFE"

echo "==> nfpm package (deb) VERSION=$VERSION → $OUT_DIR"
(
  cd "$ROOT"
  nfpm package -f packaging/nfpm.yaml -p deb -t "$OUT_DIR"
)

DEB="$(find "$OUT_DIR" -maxdepth 1 -name 'mount-wrapper_*.deb' -type f | head -n 1)"
if [[ -z "$DEB" || ! -f "$DEB" ]]; then
  echo "smoke-package-contents: no .deb produced in $OUT_DIR" >&2
  ls -la "$OUT_DIR" >&2 || true
  exit 1
fi
echo "==> deb: $DEB"

LISTING="$(dpkg-deb -c "$DEB")"
echo "==> dpkg-deb -c (paths only):"
printf '%s\n' "$LISTING" | awk '{print $NF}' | sort -u | sed 's/^/  /'

# dpkg-deb -c lines end with the member path (./usr/...).
missing=0
for path in "${REQUIRED_DEB_PATHS[@]}"; do
  # Match path as a whole last field (allow trailing / for dirs, though we ship files).
  if ! printf '%s\n' "$LISTING" | awk '{print $NF}' | grep -qxF "$path"; then
    echo "MISSING required path in deb: $path" >&2
    missing=1
  else
    echo "OK  $path"
  fi
done

for path in "${EXTRA_DEB_PATHS[@]}"; do
  if ! printf '%s\n' "$LISTING" | awk '{print $NF}' | grep -qxF "$path"; then
    echo "WARN extra path not in deb (expected for goreleaser parity): $path" >&2
    # Treat extras as required for inventory smoke so nfpm/goreleaser stay aligned.
    missing=1
  else
    echo "OK  $path"
  fi
done

if [[ "$missing" -ne 0 ]]; then
  echo "smoke-package-contents: deb inventory FAILED" >&2
  exit 1
fi
echo "==> deb inventory OK"

# Optional tar inventory (release-style archives from goreleaser / package-musl).
pick_tar() {
  if [[ -n "$PACKAGE_TAR" ]]; then
    printf '%s\n' "$PACKAGE_TAR"
    return
  fi
  if [[ "$CHECK_TAR" != "1" ]]; then
    return
  fi
  # Prefer primary CGO=0 linux amd64 tarball under dist/.
  local candidates
  candidates="$(find dist -maxdepth 1 -name 'mount-wrapper_*_linux_amd64.tar.gz' ! -name '*_musl.tar.gz' -type f 2>/dev/null | sort | tail -n 1 || true)"
  if [[ -n "$candidates" ]]; then
    printf '%s\n' "$candidates"
  fi
}

TAR_PATH="$(pick_tar || true)"
if [[ -n "${TAR_PATH:-}" ]]; then
  if [[ ! -f "$TAR_PATH" ]]; then
    echo "smoke-package-contents: PACKAGE_TAR not found: $TAR_PATH" >&2
    exit 1
  fi
  echo "==> tar inventory: $TAR_PATH"
  # Normalize: strip leading ./ and collapse to basename-path for matching.
  TAR_LIST="$(tar -tzf "$TAR_PATH" | sed 's|^\./||')"
  # Tarballs use relative layout (packaging/…), not FHS install paths.
  REQUIRED_TAR_MEMBERS=(
    "mount-wrapper"
    "packaging/systemd/mount-wrapper.service"
    "packaging/examples/config.yaml.example"
    "packaging/scripts/seed-config.sh"
    "packaging/scripts/create-user.sh"
    "packaging/man/mount-wrapper.1"
  )
  tar_missing=0
  for mem in "${REQUIRED_TAR_MEMBERS[@]}"; do
    if printf '%s\n' "$TAR_LIST" | grep -qxF "$mem" \
      || printf '%s\n' "$TAR_LIST" | grep -qE "(^|/)${mem}$"; then
      echo "OK  tar $mem"
    else
      echo "MISSING required member in tar: $mem" >&2
      tar_missing=1
    fi
  done
  if [[ "$tar_missing" -ne 0 ]]; then
    echo "smoke-package-contents: tar inventory FAILED" >&2
    exit 1
  fi
  echo "==> tar inventory OK"
elif [[ "$CHECK_TAR" == "1" ]]; then
  echo "==> CHECK_TAR=1 but no dist/*_linux_amd64.tar.gz found; skipping tar (deb OK)"
fi

echo "==> smoke-package-contents OK"
