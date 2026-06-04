#!/usr/bin/env bash
# Regenerate TypeScript stubs from the shared proto/ tree using buf + ts-proto.
#
# Run from sdks/node/ via `bun run codegen` or directly:
#   ./scripts/codegen.sh
#
# Output: src/generated/graph/v1/{graph,replication}.ts
#
# Requires:
#   - buf on PATH (https://buf.build/docs/installation)
#   - node_modules installed (`bun install`) so the ts-proto plugin resolves.

set -euo pipefail

cd "$(dirname "$0")/.."

PLUGIN="./node_modules/.bin/protoc-gen-ts_proto"
OUT_DIR="src/generated"

if [[ ! -x "$PLUGIN" ]]; then
  echo "error: $PLUGIN not found — run 'bun install' first." >&2
  exit 1
fi

if ! command -v buf >/dev/null 2>&1; then
  echo "error: buf not on PATH — install from https://buf.build/docs/installation" >&2
  exit 1
fi

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

buf generate --template buf.gen.yaml

echo "✓ TypeScript stubs regenerated into $OUT_DIR"
