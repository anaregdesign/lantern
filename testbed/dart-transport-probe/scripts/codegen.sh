#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
connect_generated="$repo_root/testbed/dart-transport-probe/connect/lib/src/gen"
grpc_generated="$repo_root/testbed/dart-transport-probe/grpc/lib/src/gen"

rm -rf "$connect_generated" "$grpc_generated"
mkdir -p "$connect_generated" "$grpc_generated"
cd "$repo_root"
buf generate --template testbed/dart-transport-probe/connect/buf.gen.yaml
buf generate --template testbed/dart-transport-probe/grpc/buf.gen.yaml
