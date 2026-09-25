#!/usr/bin/env bash
set -euo pipefail

sdk_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
repo_root=$(cd "$sdk_root/../.." && pwd)
generated="$sdk_root/lib/src/gen"

rm -rf "$generated"
mkdir -p "$generated"
cd "$repo_root"

if command -v buf >/dev/null 2>&1; then
  buf generate --template sdks/dart/buf.gen.yaml
else
  go run github.com/bufbuild/buf/cmd/buf@v1.71.0 generate \
    --template sdks/dart/buf.gen.yaml
fi
