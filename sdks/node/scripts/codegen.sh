#!/usr/bin/env bash
# Regenerate TypeScript stubs from the shared proto/ tree using buf.
#
# Run from sdks/node/ via `bun run codegen` or directly:
#   ./scripts/codegen.sh
#
# Output:
#   - src/gen/graph/v1/{graph,replication}_pb.ts (protobuf-es v2 message
#     classes + Connect-ES service schema descriptors consumed by
#     src/client.ts via createClient(LanternService, transport))
#
# Requires:
#   - buf on PATH (https://buf.build/docs/installation)
#   - node_modules installed (`bun install`) so the protoc-gen-es plugin
#     resolves.

set -euo pipefail

cd "$(dirname "$0")/.."

PLUGIN="./node_modules/.bin/protoc-gen-es"
OUT="src/gen"

if [[ ! -x "$PLUGIN" ]]; then
  echo "error: $PLUGIN not found — run 'bun install' first." >&2
  exit 1
fi

if ! command -v buf >/dev/null 2>&1; then
  echo "error: buf not on PATH — install from https://buf.build/docs/installation" >&2
  exit 1
fi

rm -rf "$OUT"
mkdir -p "$OUT"

buf generate --template buf.gen.yaml

echo "✓ TypeScript stubs regenerated → $OUT"
