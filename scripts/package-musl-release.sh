#!/usr/bin/env bash
# Package Alpine-built musl/static binaries into optional release tarballs.
#
# Primary GoReleaser matrix stays CGO_ENABLED=0 pure-Go (no second goreleaser
# build id). This script only renames/archives pre-built binaries from
# scripts/build-musl.sh into release-friendly names:
#
#   mount-wrapper_${VERSION}_linux_amd64_musl.tar.gz
#   mount-wrapper_${VERSION}_linux_arm64_musl.tar.gz
#
# Usage (repo root, after build-musl):
#   ARCHS=amd64,arm64 ./scripts/build-musl.sh
#   ./scripts/package-musl-release.sh
#
# Env:
#   VERSION      Release version string (default: dist/metadata.json .version,
#                else git describe --tags --always, else "dev")
#   BIN_DIR      Directory with mount-wrapper-linux-*-musl (default: bin)
#   OUT_DIR      Output directory for tarballs (default: dist)
#   ARCHS        Comma list to package (default: amd64,arm64; skips missing)
#   UPDATE_SUMS  Append/replace musl lines in OUT_DIR/SHA256SUMS (default: 1)
#   REQUIRE_ALL  If 1, fail when a listed ARCH binary is missing (default: 0)
#
# Exit 0 on success (or no binaries found when REQUIRE_ALL=0 and nothing built).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BIN_DIR="${BIN_DIR:-$ROOT/bin}"
OUT_DIR="${OUT_DIR:-$ROOT/dist}"
ARCHS="${ARCHS:-amd64,arm64}"
UPDATE_SUMS="${UPDATE_SUMS:-1}"
REQUIRE_ALL="${REQUIRE_ALL:-0}"

version_from_metadata() {
  local meta="$OUT_DIR/metadata.json"
  if [[ -f "$meta" ]] && command -v python3 >/dev/null 2>&1; then
    python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("version",""))' "$meta" 2>/dev/null || true
  elif [[ -f "$meta" ]] && command -v jq >/dev/null 2>&1; then
    jq -r '.version // empty' "$meta" 2>/dev/null || true
  fi
}

if [[ -z "${VERSION:-}" ]]; then
  VERSION="$(version_from_metadata)"
fi
if [[ -z "${VERSION:-}" ]]; then
  VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
fi

mkdir -p "$OUT_DIR"

IFS=',' read -r -a ARCH_LIST <<<"$ARCHS"
PACKAGED=()
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

# Extra files bundled like primary archives (subset; operators already have docs).
EXTRA_FILES=(LICENSE README.md docs/install.md)

for arch in "${ARCH_LIST[@]}"; do
  arch="$(echo "$arch" | tr -d '[:space:]')"
  [[ -n "$arch" ]] || continue
  case "$arch" in
    amd64|arm64) ;;
    *)
      echo "package-musl-release: unsupported ARCH '$arch'" >&2
      exit 1
      ;;
  esac

  src="${BIN_DIR}/mount-wrapper-linux-${arch}-musl"
  if [[ ! -f "$src" ]]; then
    if [[ "$REQUIRE_ALL" == "1" ]]; then
      echo "package-musl-release: missing $src (run ARCHS=… ./scripts/build-musl.sh)" >&2
      exit 1
    fi
    echo "package-musl-release: skip linux/${arch} (no $src)"
    continue
  fi
  if [[ ! -x "$src" ]]; then
    chmod +x "$src" || true
  fi

  work="${STAGE}/${arch}"
  rm -rf "$work"
  mkdir -p "$work"
  cp -f "$src" "${work}/mount-wrapper"
  chmod 755 "${work}/mount-wrapper"

  for f in "${EXTRA_FILES[@]}"; do
    if [[ -f "$ROOT/$f" ]]; then
      # Flat layout next to the binary (basename only).
      cp -f "$ROOT/$f" "${work}/$(basename "$f")"
    fi
  done

  # Marker so operators know this is the optional Alpine/musl path.
  cat >"${work}/MUSL.txt" <<EOF
Optional Alpine (musl-env) static build of mount-wrapper.

Built via scripts/build-musl.sh (golang:*-alpine, CGO_ENABLED=0).
Primary release binaries are pure-Go CGO_ENABLED=0 from GoReleaser and are
already static; this artifact is an explicit musl-path name for Rocky/Alpine
operators who prefer the D7 path.

Version: ${VERSION}
Arch:    linux/${arch}
EOF

  archive_name="mount-wrapper_${VERSION}_linux_${arch}_musl.tar.gz"
  archive_path="${OUT_DIR}/${archive_name}"

  echo "==> package ${archive_name}"
  tar -C "$work" -czf "$archive_path" .
  PACKAGED+=("$archive_path")
done

if [[ ${#PACKAGED[@]} -eq 0 ]]; then
  echo "package-musl-release: no musl binaries packaged under $BIN_DIR" >&2
  exit 1
fi

if [[ "$UPDATE_SUMS" == "1" ]]; then
  sums="${OUT_DIR}/SHA256SUMS"
  # Drop any previous musl lines so re-runs are idempotent, then append.
  if [[ -f "$sums" ]]; then
    tmp="$(mktemp)"
    # Match both basename and any path form; checksum files use basenames.
    grep -vE '_linux_(amd64|arm64)_musl\.tar\.gz$' "$sums" >"$tmp" || true
    mv "$tmp" "$sums"
  else
    : >"$sums"
  fi
  (
    cd "$OUT_DIR"
    for p in "${PACKAGED[@]}"; do
      base="$(basename "$p")"
      sha256sum "$base"
    done
  ) >>"$sums"
  echo "==> updated $sums (musl entries)"
fi

echo "==> package-musl-release OK:"
for p in "${PACKAGED[@]}"; do
  ls -la "$p"
done
