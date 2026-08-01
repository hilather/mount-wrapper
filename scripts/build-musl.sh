#!/usr/bin/env bash
# Build a fully static Linux mount-wrapper binary inside golang:*-alpine
# (musl-based toolchain environment). Extra D7 path — does **not** replace
# the default GoReleaser CGO_ENABLED=0 matrix.
#
# Why Alpine if the project is pure-Go (modernc.org/sqlite)?
#   CGO_ENABLED=0 already yields a statically linked binary on any Linux host.
#   Building inside Alpine gives a reproducible musl-environment CI path, an
#   optional artifact name for operators who want "static/musl" explicit, and
#   a place to add CGO+musl-gcc later if a dependency ever needs libc.
#
# Usage (repo root, needs docker or podman):
#   ./scripts/build-musl.sh                 # linux/amd64 → bin/mount-wrapper-linux-amd64-musl
#   ARCHS=amd64,arm64 ./scripts/build-musl.sh
#   GO_IMAGE=golang:1.25-alpine ./scripts/build-musl.sh
#   VERIFY=0 ./scripts/build-musl.sh        # skip file/ldd checks
#
# Release packaging (optional tarballs; used by release.yml after GoReleaser):
#   make package-musl   # or: ./scripts/package-musl-release.sh
#   → dist/mount-wrapper_${VERSION}_linux_{amd64,arm64}_musl.tar.gz
#
# Then smoke (static runs on Alpine and Rocky/glibc):
#   BIN=./bin/mount-wrapper-linux-amd64-musl ./scripts/smoke-binary.sh
#   make smoke-rocky   # after copying/symlink to bin/mount-wrapper, or mount by path
#
# Exit 0 on success.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

RUNTIME=""
if command -v docker >/dev/null 2>&1; then
  RUNTIME=docker
elif command -v podman >/dev/null 2>&1; then
  RUNTIME=podman
else
  echo "build-musl: need docker or podman" >&2
  exit 1
fi

# Match go.mod major.minor; override with GO_IMAGE if needed.
GO_IMAGE="${GO_IMAGE:-golang:1.25-alpine}"
ARCHS="${ARCHS:-amd64}"
VERIFY="${VERIFY:-1}"
OUT_DIR="${OUT_DIR:-$ROOT/bin}"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}"

mkdir -p "$OUT_DIR"

# Module + build cache mounts (best-effort; empty dirs work first time).
GOMODCACHE_HOST="${GOMODCACHE_HOST:-${HOME}/.cache/go-mod-musl}"
GOCACHE_HOST="${GOCACHE_HOST:-${HOME}/.cache/go-build-musl}"
mkdir -p "$GOMODCACHE_HOST" "$GOCACHE_HOST"

IFS=',' read -r -a ARCH_LIST <<<"$ARCHS"
BUILT=()

for arch in "${ARCH_LIST[@]}"; do
  arch="$(echo "$arch" | tr -d '[:space:]')"
  [[ -n "$arch" ]] || continue
  case "$arch" in
    amd64|arm64) ;;
    *)
      echo "build-musl: unsupported ARCH '$arch' (use amd64 and/or arm64)" >&2
      exit 1
      ;;
  esac

  out_name="mount-wrapper-linux-${arch}-musl"
  out_path="${OUT_DIR}/${out_name}"
  # Write under /out in the container so the host bind is the only writable path.
  cont_out="/out/${out_name}"

  echo "==> musl/static build: linux/${arch} via ${GO_IMAGE} → ${out_path}"

  $RUNTIME run --rm \
    -e CGO_ENABLED=0 \
    -e GOOS=linux \
    -e GOARCH="${arch}" \
    -e GOFLAGS="-trimpath" \
    -e GOMODCACHE=/go/pkg/mod \
    -e GOCACHE=/root/.cache/go-build \
    -e "MW_LDFLAGS=${LDFLAGS}" \
    -e "MW_OUT=${cont_out}" \
    -v "$ROOT:/src:ro" \
    -v "$OUT_DIR:/out" \
    -v "$GOMODCACHE_HOST:/go/pkg/mod" \
    -v "$GOCACHE_HOST:/root/.cache/go-build" \
    -w /src \
    "$GO_IMAGE" \
    sh -ec 'set -e
      go version
      go build -ldflags "$MW_LDFLAGS" -o "$MW_OUT" ./cmd/mount-wrapper
      chmod 755 "$MW_OUT"
    '

  if [[ ! -x "$out_path" ]]; then
    echo "build-musl: missing output $out_path" >&2
    exit 1
  fi

  if [[ "$VERIFY" == "1" ]]; then
    echo "==> verify static: $out_path"
    if command -v file >/dev/null 2>&1; then
      file_out="$(file "$out_path")"
      echo "$file_out"
      if ! grep -qiE 'statically linked|static-pie' <<<"$file_out"; then
        echo "build-musl: expected statically linked binary; file(1) said:" >&2
        echo "$file_out" >&2
        exit 1
      fi
    else
      echo "build-musl: file(1) not found; skipping file check" >&2
    fi
    # ldd should fail or report "not a dynamic executable".
    if command -v ldd >/dev/null 2>&1; then
      ldd_tmp="$(mktemp)"
      if ldd "$out_path" >"$ldd_tmp" 2>&1; then
        # Some ldd versions exit 0 with "not a dynamic executable".
        if grep -qiE 'not a dynamic|statically linked' "$ldd_tmp"; then
          cat "$ldd_tmp"
        else
          echo "build-musl: expected non-dynamic binary; ldd said:" >&2
          cat "$ldd_tmp" >&2
          rm -f "$ldd_tmp"
          exit 1
        fi
      else
        # Non-zero is the usual "not a dynamic executable" path.
        cat "$ldd_tmp" || true
      fi
      rm -f "$ldd_tmp"
    fi
  fi

  BUILT+=("$out_path")
done

# Convenience symlink/copy for host arch when building a single matching arch.
host_arch="$(uname -m)"
case "$host_arch" in
  x86_64) host_goarch=amd64 ;;
  aarch64|arm64) host_goarch=arm64 ;;
  *) host_goarch="" ;;
esac
if [[ -n "$host_goarch" ]] && [[ -x "${OUT_DIR}/mount-wrapper-linux-${host_goarch}-musl" ]]; then
  cp -f "${OUT_DIR}/mount-wrapper-linux-${host_goarch}-musl" "${OUT_DIR}/mount-wrapper-musl"
  chmod 755 "${OUT_DIR}/mount-wrapper-musl"
  echo "==> also: ${OUT_DIR}/mount-wrapper-musl (host ${host_goarch})"
fi

echo "==> build-musl OK:"
for p in "${BUILT[@]}"; do
  ls -la "$p"
done
