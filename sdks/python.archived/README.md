# lantern-sdk-python (archived)

**Archived 2026-06-06** pending Connect-Python stabilization. See
[#341](https://github.com/anaregdesign/lantern/issues/341) and the
parent Connect-only migration epic
[#335](https://github.com/anaregdesign/lantern/issues/335).

The grpc-based Python SDK that used to live at `sdks/python/` has
been moved here without modification so historical tags
(`sdks/python/v0.1.x`) still resolve. The code is **no longer
built, tested, or published** — CI is removed and PyPI publishes
are intentionally not cut. A future v1.0 Python SDK built on
Connect-Python will replace it once that ecosystem (currently
alpha) stabilizes; that work is tracked separately as a new issue
opened when this archive lands.

## Interim usage path: HTTP+JSON over Connect

The Lantern server speaks the Connect protocol on its primary
`:6380` port (after [#347](https://github.com/anaregdesign/lantern/issues/347))
and on its additive `LANTERN_CONNECT_PORT` listener
([#337](https://github.com/anaregdesign/lantern/issues/337)) today.
Connect+JSON is the simplest interop path for Python — POST to
`/{service}/{method}` with a JSON body, get a JSON response back.
No codegen required.

### Unary RPC (most calls)

```python
import httpx

BASE = "http://lantern:6381"  # or :6380 after #347

def get_vertex(key: str) -> dict | None:
    r = httpx.post(
        f"{BASE}/graph.v1.LanternService/GetVertex",
        json={"key": key},
        headers={"Content-Type": "application/json"},
        timeout=5.0,
    )
    if r.status_code == 404 or (r.is_success and r.json().get("vertex") is None):
        return None
    r.raise_for_status()
    return r.json().get("vertex")

def put_vertex(key: str, value: str) -> None:
    httpx.post(
        f"{BASE}/graph.v1.LanternService/PutVertex",
        json={"vertex": {"key": key, "string": value}},
        headers={"Content-Type": "application/json"},
        timeout=5.0,
    ).raise_for_status()
```

The protobuf JSON spec puts oneof fields flat on the message, so
`{"key": "...", "string": "..."}` (not `{"value": {"string": "..."}}`)
is the correct payload shape for `Vertex.string`. The full kind
set — `float64`, `float32`, `int32`, `int64`, `uint32`, `uint64`,
`bool`, `string`, `bytes` (base64), `timestamp` (ISO 8601),
`duration` (Go duration format), `nil` (boolean `true`) — is
documented in [`pb/graph/v1/graph.proto`](../../pb/graph/v1/graph.proto).

### Streaming RPCs (`Illuminate`, `Subscribe`, `Snapshot`)

The Connect protocol's server-streaming wire format is a sequence
of length-prefixed envelopes over a single HTTP response body.
The simplest portable approach is to use `httpx.stream`:

```python
import json
import struct
import httpx

def illuminate(seed: str, *, step: int = 2, k: int = 10) -> dict:
    """One-shot Illuminate call. Returns the merged Graph dict."""
    with httpx.stream(
        "POST",
        f"{BASE}/graph.v1.LanternService/Illuminate",
        json={"seed": seed, "step": step, "k": k},
        headers={"Content-Type": "application/json"},
        timeout=30.0,
    ) as r:
        r.raise_for_status()
        # Illuminate is unary today; if it ever becomes streaming,
        # the response body is a series of 5-byte framed envelopes.
        return r.json()
```

For long-lived `Subscribe`, frame the response body as Connect's
streaming envelopes (1 byte flags + 4-byte big-endian length +
payload). The
[Connect protocol reference](https://connectrpc.com/docs/protocol#streaming-response)
documents the exact frame layout.

### Error semantics

A non-2xx response carries a JSON body shaped
`{"code": "not_found", "message": "..."}`. The 16-entry code set
matches gRPC's status codes verbatim:
```python
import httpx

try:
    r = httpx.post(...)
    r.raise_for_status()
except httpx.HTTPStatusError as e:
    body = e.response.json()
    if body.get("code") == "not_found":
        ...
```

### Message shapes

The canonical wire schemas live in
[`pb/graph/v1/graph.proto`](../../pb/graph/v1/graph.proto) and
[`pb/graph/v1/replication.proto`](../../pb/graph/v1/replication.proto).
For a typed Python representation, the easiest path is to run
`buf generate` with a Python plugin (e.g.
[`grpcio-tools`](https://pypi.org/project/grpcio-tools/) or
[`mypy-protobuf`](https://pypi.org/project/mypy-protobuf/))
against `proto/` locally — no Lantern-side codegen step is needed.

---

## Historical content

The original `README.md`, source layout (`lantern_client/`, `src/`,
`tests/`, `examples/`), and `pyproject.toml` are preserved as-is
in this directory. They are documentation only; no part of this
tree is wired into the workspace's build, test, or release flows
after the archive landed in
[#341](https://github.com/anaregdesign/lantern/issues/341).
