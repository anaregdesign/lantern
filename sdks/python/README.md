# lantern-client

Python client SDK for [Lantern](https://github.com/anaregdesign/lantern), an
in-memory graph key-vertex-store served over gRPC. Mirrors the [Go
SDK](https://github.com/anaregdesign/lantern/tree/main/sdks/go) feature set:
single + batch vertex/edge operations, prefix scans, prefix delete,
Illuminate (k-bounded BFS with TF-IDF / MST / SPT post-processing), and the
gRPC health check.

> v0.1.x is **sync-only** (`grpc` blocking stubs). An `asyncio` companion
> built on `grpc.aio` is planned for v0.2; see the
> [tracking issue](https://github.com/anaregdesign/lantern/issues/270).

## Install

```sh
pip install lantern-client
# or with uv
uv add lantern-client
```

## Quick start

```python
from lantern_client import Lantern

with Lantern.connect("localhost:6380") as cli:
    cli.put_vertex("user:1", {"name": "alice"}, ttl_seconds=3600)
    v = cli.get_vertex("user:1")
    print(v)  # Vertex(key='user:1', value={'name': 'alice'}, ...)

    cli.add_edge("user:1", "post:42", weight=1.5, ttl_seconds=3600)
    graph = cli.illuminate("user:1", step=2, k=10)
    print(graph.vertices, graph.edges)
```

`Lantern.connect` accepts the same target forms as the Go SDK:

- `"host:port"` — single endpoint.
- `"dns:///host:port"` — DNS fan-out with round-robin load balancing.
- `Lantern.connect_endpoints([...])` — explicit static endpoint list.

See [examples/quickstart.py](examples/quickstart.py) for the full surface.

## Value types

`put_vertex` accepts native Python values; the SDK picks the right protobuf
oneof variant. Supported types: `int`, `float`, `bool`, `str`, `bytes`,
`datetime.datetime`, `datetime.timedelta`, `None` (tombstone).

`int` values land in `int64` or `uint64` based on sign; `float` always lands
in `float64`. Use the explicit constructors in `lantern_client.values`
(`int32`, `uint32`, `float32`) to pin a narrower wire type. Reading is
symmetric — `vertex.value` returns the natural Python type; `vertex.kind` is
the `VertexKind` enum if you need to dispatch without `isinstance`.

## Codegen

Generated protobuf / gRPC stubs live under `src/lantern_client/_pb/` and are
committed to the repo, mirroring how Lantern's Go module keeps `pb/`
in-tree. To regenerate after a `.proto` change:

```sh
uv run --dev scripts/codegen.sh
```

## License

Apache-2.0 — see [LICENSE](LICENSE).
