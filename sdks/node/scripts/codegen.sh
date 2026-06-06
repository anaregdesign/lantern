#!/usr/bin/env bash
# Regenerate TypeScript stubs from the shared proto/ tree using buf.
#
# Run from sdks/node/ via `bun run codegen` or directly:
#   ./scripts/codegen.sh
#
# Outputs:
#   - src/generated/graph/v1/{graph,replication}.ts (legacy ts-proto +
#     grpc-js stubs consumed by src/client.ts)
#   - src/gen/graph/v1/{graph,replication}_{pb,connect}.ts (Connect-ES
#     stubs consumed by the new LanternConnect class introduced in
#     #340)
#
# Requires:
#   - buf on PATH (https://buf.build/docs/installation)
#   - node_modules installed (`bun install`) so the ts-proto plugin
#     resolves.

set -euo pipefail

cd "$(dirname "$0")/.."

PLUGIN="./node_modules/.bin/protoc-gen-ts_proto"
LEGACY_OUT="src/generated"
CONNECT_OUT="src/gen"

if [[ ! -x "$PLUGIN" ]]; then
  echo "error: $PLUGIN not found — run 'bun install' first." >&2
  exit 1
fi

if ! command -v buf >/dev/null 2>&1; then
  echo "error: buf not on PATH — install from https://buf.build/docs/installation" >&2
  exit 1
fi

rm -rf "$LEGACY_OUT" "$CONNECT_OUT"
mkdir -p "$LEGACY_OUT" "$CONNECT_OUT"

buf generate --template buf.gen.yaml

echo "✓ TypeScript stubs regenerated:"
echo "    legacy (ts-proto + grpc-js)   → $LEGACY_OUT"
echo "    Connect-ES + protobuf-es      → $CONNECT_OUT"
