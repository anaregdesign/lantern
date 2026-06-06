#!/usr/bin/env bash
# Regenerate Python protobuf + gRPC stubs from proto/graph/v1/*.proto.
#
# Usage (from sdks/python/):
#   uv run scripts/codegen.sh
#
# Inputs:  ../../proto/graph/v1/graph.proto
# Outputs: src/lantern_client/_pb/graph/v1/{graph_pb2.py, graph_pb2_grpc.py, graph_pb2.pyi}
#
# Generated stubs are committed to the repo (mirroring how Lantern's Go module
# keeps `pb/` in-tree) so end users do not need protoc installed.
set -euo pipefail

cd "$(dirname "$0")/.."

PROTO_ROOT="../../proto"
OUT_ROOT="src/lantern_client/_pb"

# google/api/*.proto come from googleapis-common-protos (dev dep).
GOOGLE_API_INCLUDE="$(python -c 'import google.api, pathlib; print(pathlib.Path(next(iter(google.api.__path__))).parent.parent)')"

rm -rf "${OUT_ROOT}"
mkdir -p "${OUT_ROOT}"

python -m grpc_tools.protoc \
    -I"${PROTO_ROOT}" \
    -I"${GOOGLE_API_INCLUDE}" \
    --python_out="${OUT_ROOT}" \
    --pyi_out="${OUT_ROOT}" \
    --grpc_python_out="${OUT_ROOT}" \
    "${PROTO_ROOT}"/graph/v1/*.proto

# grpc_tools emits absolute imports rooted at the include path. Rewrite them
# to package-relative imports under lantern_client._pb so the generated code
# works when installed as a package.
find "${OUT_ROOT}" -name '*.py' -print0 | while IFS= read -r -d '' f; do
    python - "$f" <<'PY'
import re, sys, pathlib
p = pathlib.Path(sys.argv[1])
src = p.read_text()
src = re.sub(
    r'^from graph\.v1 import (\w+_pb2) as (\w+)$',
    r'from lantern_client._pb.graph.v1 import \1 as \2',
    src, flags=re.MULTILINE,
)
src = re.sub(
    r'^import graph\.v1\.(\w+_pb2)$',
    r'import lantern_client._pb.graph.v1.\1',
    src, flags=re.MULTILINE,
)
p.write_text(src)
PY
done

# Ensure every directory is an importable package.
find "${OUT_ROOT}" -type d -print0 | while IFS= read -r -d '' d; do
    touch "${d}/__init__.py"
done

# Marker README so consumers can `git grep` the generation status.
cat > "${OUT_ROOT}/README.md" <<'MD'
# Generated stubs

This directory is generated from `proto/graph/v1/*.proto` by
`scripts/codegen.sh`. Do not edit by hand. Re-run the script after any
`.proto` change and commit the resulting diff.
MD

echo "OK: regenerated ${OUT_ROOT}"
