#!/usr/bin/env bash
# Fill version + darwin sha256 digests into the Homebrew formula sketch.
#
# Reads SHA256SUMS (GoReleaser layout) for:
#   mount-wrapper_${VERSION}_darwin_arm64.tar.gz
#   mount-wrapper_${VERSION}_darwin_amd64.tar.gz
#
# Usage (repo root):
#   ./scripts/update-homebrew-formula.sh
#   ./scripts/update-homebrew-formula.sh 0.1.1
#   ./scripts/update-homebrew-formula.sh 0.1.1 dist/SHA256SUMS
#   ./scripts/update-homebrew-formula.sh 0.1.1 dist/SHA256SUMS /tmp/mount-wrapper.rb
#
# Env (overrides positional args when set):
#   VERSION      Release version without leading v (e.g. 0.1.1). Default: formula
#                version line, else git describe stripped of leading v, else 0.1.1
#   SHA256SUMS   Checksums file (default: dist/SHA256SUMS)
#   FORMULA      Input formula path
#                (default: packaging/homebrew/mount-wrapper.rb.example)
#   OUT          Write destination (default: in-place FORMULA)
#   DRY_RUN      If 1, print rewritten formula to stdout; do not write OUT
#
# Does not run brew. Exit non-zero if a required digest is missing.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

FORMULA="${FORMULA:-$ROOT/packaging/homebrew/mount-wrapper.rb.example}"
# Track whether caller set SHA256SUMS/OUT via env (positional must not clobber).
_sums_from_env=0
_out_from_env=0
[[ -n "${SHA256SUMS+x}" && -n "${SHA256SUMS}" ]] && _sums_from_env=1
[[ -n "${OUT+x}" && -n "${OUT}" ]] && _out_from_env=1
SHA256SUMS="${SHA256SUMS:-$ROOT/dist/SHA256SUMS}"
DRY_RUN="${DRY_RUN:-0}"

# Positional: [VERSION] [SHA256SUMS] [OUT]
# Env wins over positionals when set (do not shift VERSION if already provided).
if [[ -z "${VERSION:-}" && "${1:-}" != "" && "${1:-}" != -* ]]; then
  VERSION="$1"
  shift
fi
if [[ "$_sums_from_env" -eq 0 && "${1:-}" != "" && "${1:-}" != -* ]]; then
  SHA256SUMS="$1"
  shift
fi
if [[ "$_out_from_env" -eq 0 && "${1:-}" != "" && "${1:-}" != -* ]]; then
  OUT="$1"
  shift
fi

if [[ ! -f "$FORMULA" ]]; then
  echo "update-homebrew-formula: formula not found: $FORMULA" >&2
  exit 1
fi

if [[ -z "${VERSION:-}" ]]; then
  # Prefer existing formula version line.
  VERSION="$(sed -n 's/^[[:space:]]*version "\([^"]*\)".*/\1/p' "$FORMULA" | head -n1 || true)"
fi
if [[ -z "${VERSION:-}" ]]; then
  VERSION="$(git describe --tags --always --dirty 2>/dev/null || true)"
fi
# Strip leading v from tags (v0.1.1 → 0.1.1); leave non-tag describe as-is if no v.
VERSION="${VERSION#v}"
if [[ -z "${VERSION:-}" || "$VERSION" == "dev" ]]; then
  VERSION="0.1.1"
fi

if [[ ! -f "$SHA256SUMS" ]]; then
  echo "update-homebrew-formula: SHA256SUMS not found: $SHA256SUMS" >&2
  echo "  (pass path as arg 2 or set SHA256SUMS=; produce via make release-snapshot)" >&2
  exit 1
fi

# Extract sha256 for a basename from GNU/BSD-style SUMS lines:
#   <hex>  file
#   <hex> *file
#   <hex>  path/to/file
digest_for() {
  local want="$1" sums="$2" line hash rest base
  while IFS= read -r line || [[ -n "$line" ]]; do
    # Skip comments / blank
    [[ -z "${line// }" ]] && continue
    [[ "$line" == \#* ]] && continue
    hash="${line%% *}"
    rest="${line#"$hash"}"
    rest="${rest#"${rest%%[![:space:]]*}"}" # ltrim
    # Drop optional leading *
    rest="${rest#\*}"
    base="$(basename "$rest")"
    if [[ "$base" == "$want" ]]; then
      # Validate 64-hex digest
      if [[ ! "$hash" =~ ^[0-9a-fA-F]{64}$ ]]; then
        echo "update-homebrew-formula: bad digest for $want: $hash" >&2
        exit 1
      fi
      # Homebrew wants lowercase hex
      printf '%s\n' "$(printf '%s' "$hash" | tr 'A-F' 'a-f')"
      return 0
    fi
  done <"$sums"
  return 1
}

ARM_NAME="mount-wrapper_${VERSION}_darwin_arm64.tar.gz"
AMD_NAME="mount-wrapper_${VERSION}_darwin_amd64.tar.gz"

ARM_SHA="$(digest_for "$ARM_NAME" "$SHA256SUMS" || true)"
AMD_SHA="$(digest_for "$AMD_NAME" "$SHA256SUMS" || true)"

if [[ -z "$ARM_SHA" ]]; then
  echo "update-homebrew-formula: missing $ARM_NAME in $SHA256SUMS" >&2
  exit 1
fi
if [[ -z "$AMD_SHA" ]]; then
  echo "update-homebrew-formula: missing $AMD_NAME in $SHA256SUMS" >&2
  exit 1
fi

OUT="${OUT:-$FORMULA}"

# Rewrite: version line + sha256 after each darwin_* URL (or known placeholders).
# Pure awk so we do not depend on GNU sed -i.
rewritten="$(
  VERSION="$VERSION" ARM_SHA="$ARM_SHA" AMD_SHA="$AMD_SHA" awk '
    BEGIN {
      ver = ENVIRON["VERSION"]
      arm = ENVIRON["ARM_SHA"]
      amd = ENVIRON["AMD_SHA"]
    }
    /^[[:space:]]*version "/ {
      sub(/version "[^"]*"/, "version \"" ver "\"")
      print
      next
    }
    /REPLACE_ME_DARWIN_ARM64/ {
      gsub(/REPLACE_ME_DARWIN_ARM64/, arm)
      print
      next
    }
    /REPLACE_ME_DARWIN_AMD64/ {
      gsub(/REPLACE_ME_DARWIN_AMD64/, amd)
      print
      next
    }
    /darwin_arm64\.tar\.gz/ {
      print
      if ((getline nextline) > 0) {
        if (nextline ~ /sha256[[:space:]]+"/) {
          match(nextline, /^[[:space:]]*/)
          indent = substr(nextline, RSTART, RLENGTH)
          print indent "sha256 \"" arm "\""
        } else {
          print nextline
        }
      }
      next
    }
    /darwin_amd64\.tar\.gz/ {
      print
      if ((getline nextline) > 0) {
        if (nextline ~ /sha256[[:space:]]+"/) {
          match(nextline, /^[[:space:]]*/)
          indent = substr(nextline, RSTART, RLENGTH)
          print indent "sha256 \"" amd "\""
        } else {
          print nextline
        }
      }
      next
    }
    { print }
  ' "$FORMULA"
)"

if [[ "$DRY_RUN" == "1" ]]; then
  printf '%s\n' "$rewritten"
  exit 0
fi

out_dir="$(dirname "$OUT")"
mkdir -p "$out_dir"
tmp="$(mktemp "${TMPDIR:-/tmp}/mw-brew-formula.XXXXXX")"
# Ensure cleanup on failure
trap 'rm -f "$tmp"' EXIT
printf '%s\n' "$rewritten" >"$tmp"
# Preserve executable bit only if present (formulas are not +x normally).
mv "$tmp" "$OUT"
trap - EXIT

echo "update-homebrew-formula: wrote $OUT"
echo "  version=$VERSION"
echo "  arm64=$ARM_SHA  ($ARM_NAME)"
echo "  amd64=$AMD_SHA  ($AMD_NAME)"
