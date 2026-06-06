"""lantern_client — Python SDK for Lantern.

A typed sync gRPC client for the Lantern in-memory graph key-vertex-store.
Mirrors the Go SDK feature surface: single + batch vertex/edge operations,
prefix scans, prefix delete, Illuminate (k-bounded BFS), and gRPC health
checks. v0.1.x is sync-only; an `asyncio` companion is planned for v0.2.

Quick start::

    from lantern_client import Lantern

    with Lantern.connect("localhost:6380") as cli:
        cli.put_vertex("user:1", {"name": "alice"}, ttl_seconds=3600)
        v = cli.get_vertex("user:1")
        cli.add_edge("user:1", "post:42", weight=1.5, ttl_seconds=3600)
        graph = cli.illuminate("user:1", step=2, k=10)
"""

from __future__ import annotations

from .client import Lantern, default_service_config
from .errors import (
    BatchError,
    InvalidArgumentError,
    LanternError,
    NotFoundError,
    ResourceExhaustedError,
)
from .errors import (
    OverflowError as LanternOverflowError,
)
from .options import IlluminateOptions, ScanOptions
from .values import (
    Edge,
    EdgeInput,
    Graph,
    Optimization,
    Vertex,
    VertexInput,
    VertexKind,
    float32,
    int32,
    uint32,
    uint64,
)

__version__ = "0.1.0"

__all__ = [
    "BatchError",
    "Edge",
    "EdgeInput",
    "Graph",
    "IlluminateOptions",
    "InvalidArgumentError",
    "Lantern",
    "LanternError",
    "LanternOverflowError",
    "NotFoundError",
    "Optimization",
    "ResourceExhaustedError",
    "ScanOptions",
    "Vertex",
    "VertexInput",
    "VertexKind",
    "__version__",
    "default_service_config",
    "float32",
    "int32",
    "uint32",
    "uint64",
]
