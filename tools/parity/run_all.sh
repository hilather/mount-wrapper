#!/usr/bin/env bash
# Run all offline-safe parity inventory scripts.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"
export PATH="${HOME}/.local/go/bin:${PATH}"
export PARITY_OUT="${PARITY_OUT:-$DIR}"

echo "==> cli_surface.sh" >&2
bash "$DIR/cli_surface.sh"

echo "==> gen_config_keys.sh" >&2
bash "$DIR/gen_config_keys.sh"

echo "==> socket_ops.sh" >&2
bash "$DIR/socket_ops.sh"

echo "==> done" >&2
echo "Artifacts under: $PARITY_OUT" >&2
echo "Manual checklist: $DIR/feature_checklist.md" >&2
echo "Docs: $ROOT/docs/parity.md , $ROOT/docs/migration.md" >&2
