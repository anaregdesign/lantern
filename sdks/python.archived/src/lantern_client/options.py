"""Per-call options for ``Lantern`` methods.

Each dataclass groups the knobs for a single RPC family so the call sites stay
short (``cli.illuminate("u:1", opts=IlluminateOptions(step=2, k=10))``) while
keeping the Python signatures stable as the proto evolves.
"""

from __future__ import annotations

from dataclasses import dataclass

from .values import Optimization


@dataclass(slots=True)
class IlluminateOptions:
    """Knobs for :meth:`Lantern.illuminate`.

    All fields default to "no expansion / no post-processing" so an empty
    options object is equivalent to ``cli.illuminate(seed)`` with no kwargs.
    """

    step: int = 0
    """BFS depth limit (0 = server default; treated as "no expansion")."""

    k: int = 0
    """Per-hop fan-out, top-k neighbours kept at each frontier (0 = unlimited)."""

    tfidf: bool = False
    """Whether the server should TF-IDF re-weight edges before optimization."""

    optimization: Optimization = Optimization.UNSPECIFIED
    """Post-processing strategy to apply to the illuminated subgraph."""


@dataclass(slots=True)
class ScanOptions:
    """Knobs for :meth:`Lantern.scan_vertices` and :meth:`Lantern.scan_edges`.

    ``cursor`` is opaque server-issued bytes — pass the value returned by the
    previous call's ``next_cursor`` to resume; an empty cursor starts a fresh
    scan. ``limit`` of 0 yields the server's configured default per-page cap.
    """

    limit: int = 0
    cursor: bytes = b""


@dataclass(slots=True)
class EdgeScanOptions:
    """Knobs for :meth:`Lantern.scan_edges`."""

    tail_prefix: str = ""
    head_prefix: str = ""
    limit: int = 0
    cursor: bytes = b""


@dataclass(slots=True)
class DeleteByPrefixOptions:
    """Knobs for :meth:`Lantern.delete_vertices_by_prefix`.

    ``dry_run=True`` asks the server to report the count that *would* be
    deleted without actually deleting; use this before issuing a real delete.
    """

    limit: int = 0
    dry_run: bool = False


@dataclass(slots=True)
class ConnectOptions:
    """Knobs for :meth:`Lantern.connect`.

    All fields are optional. Defaults match the Go SDK: a 1000-entry batch
    chunk size and the SDK's built-in retry+round-robin service config.
    """

    default_timeout_seconds: float | None = None
    """Per-call timeout applied when the caller did not set one."""

    batch_chunk_size: int = 1000
    """Auto-chunk size for ``put_vertices`` / ``add_edges`` / ``put_edges`` / ``delete_*``."""

    service_config_json: str | None = None
    """Override the built-in retry+round_robin service config. ``None`` keeps the default."""

    compression: str | None = None
    """Per-call default compressor name (e.g. ``"gzip"``). ``None`` disables it."""

    user_agent: str | None = None
    """Optional gRPC user-agent string appended to the default."""


__all__ = [
    "ConnectOptions",
    "DeleteByPrefixOptions",
    "EdgeScanOptions",
    "IlluminateOptions",
    "ScanOptions",
]
