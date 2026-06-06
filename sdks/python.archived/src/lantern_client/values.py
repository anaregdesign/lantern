"""Value-type bridge between Python natives and protobuf ``Vertex`` / ``Edge``.

Native ↔ proto mapping (mirrors the Go SDK):

==================== ==========================
Python type           proto oneof
==================== ==========================
``int`` (≥ 0)         ``int64``  (or ``uint64`` if >= 2**63)
``int`` (< 0)         ``int64``
``float``             ``float64``
``str``               ``string``
``bool``              ``bool``
``bytes``             ``bytes``
``datetime.datetime`` ``Timestamp``
``datetime.timedelta````Duration``
``None``              ``Nil`` tombstone
==================== ==========================

To pin a narrower wire type, wrap the Python value in one of the
:class:`int32`, :class:`uint32`, :class:`uint64`, or :class:`float32`
markers — e.g. ``put_vertex("counter", int32(42), ttl_seconds=60)``.

When reading, :class:`Vertex` exposes the natural Python value via
``vertex.value`` and the underlying oneof discriminator via ``vertex.kind``
(a :class:`VertexKind` enum). A present vertex whose value was explicitly set
to ``None`` round-trips back as a ``Vertex`` whose ``value`` is ``None`` and
whose ``kind`` is :attr:`VertexKind.NIL` — distinct from a missing key, which
raises :class:`~lantern_client.NotFoundError`.
"""

from __future__ import annotations

import datetime as _dt
import enum
from dataclasses import dataclass, field
from typing import Any

from google.protobuf.duration_pb2 import Duration as _Duration
from google.protobuf.timestamp_pb2 import Timestamp as _Timestamp

from ._pb.graph.v1 import graph_pb2 as _pb
from .errors import OverflowError as _OverflowError

# ----------------------------------------------------------------------------
# Narrowing-type markers
# ----------------------------------------------------------------------------


class _PinnedNumeric:
    __slots__ = ("value",)
    _min: int = 0
    _max: int = 0

    def __init__(self, value: int) -> None:
        if not isinstance(value, int) or isinstance(value, bool):
            raise TypeError(f"{type(self).__name__} requires an int, got {type(value).__name__}")
        if value < self._min or value > self._max:
            raise _OverflowError(
                f"{value} out of range for {type(self).__name__} [{self._min}, {self._max}]"
            )
        self.value = value

    def __repr__(self) -> str:  # pragma: no cover - trivial
        return f"{type(self).__name__}({self.value})"

    def __eq__(self, other: object) -> bool:  # pragma: no cover - trivial
        return isinstance(other, type(self)) and self.value == other.value

    def __hash__(self) -> int:  # pragma: no cover - trivial
        return hash((type(self), self.value))


class int32(_PinnedNumeric):
    """Pin a Python int to the protobuf ``int32`` oneof variant."""

    _min = -(2**31)
    _max = 2**31 - 1


class uint32(_PinnedNumeric):
    """Pin a Python int to the protobuf ``uint32`` oneof variant."""

    _min = 0
    _max = 2**32 - 1


class uint64(_PinnedNumeric):
    """Pin a Python int to the protobuf ``uint64`` oneof variant.

    Useful when you need the full ``[0, 2**64-1]`` range; default Python
    ``int`` mapping uses ``int64`` for values below ``2**63`` and ``uint64``
    only for values at or above it.
    """

    _min = 0
    _max = 2**64 - 1


class float32:
    """Pin a Python float to the protobuf ``float32`` oneof variant."""

    __slots__ = ("value",)

    def __init__(self, value: float) -> None:
        if isinstance(value, bool):
            raise TypeError("float32 requires a float, got bool")
        self.value = float(value)

    def __repr__(self) -> str:  # pragma: no cover - trivial
        return f"float32({self.value})"

    def __eq__(self, other: object) -> bool:  # pragma: no cover - trivial
        return isinstance(other, float32) and self.value == other.value

    def __hash__(self) -> int:  # pragma: no cover - trivial
        return hash((float32, self.value))


# ----------------------------------------------------------------------------
# VertexKind / Optimization
# ----------------------------------------------------------------------------


class VertexKind(enum.Enum):
    """Discriminator over the protobuf ``Vertex`` oneof variants."""

    UNSET = "unset"
    FLOAT32 = "float32"
    FLOAT64 = "float64"
    INT32 = "int32"
    INT64 = "int64"
    UINT32 = "uint32"
    UINT64 = "uint64"
    BOOL = "bool"
    STRING = "string"
    BYTES = "bytes"
    TIMESTAMP = "timestamp"
    DURATION = "duration"
    NIL = "nil"


class Optimization(enum.IntEnum):
    """Server-side post-processing strategy for :meth:`Lantern.illuminate`.

    Members mirror the proto enum exactly; pass :attr:`UNSPECIFIED` to disable
    post-processing entirely.
    """

    UNSPECIFIED = _pb.OPTIMIZATION_UNSPECIFIED
    MINIMUM_SPANNING_TREE = _pb.OPTIMIZATION_MINIMUM_SPANNING_TREE
    MAXIMUM_SPANNING_TREE = _pb.OPTIMIZATION_MAXIMUM_SPANNING_TREE
    SHORTEST_PATH_TREE = _pb.OPTIMIZATION_SHORTEST_PATH_TREE
    SHORTEST_PATH_TREE_INVERSE = _pb.OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE


# ----------------------------------------------------------------------------
# Vertex / Edge — typed Python views over the proto messages
# ----------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class Vertex:
    """A vertex returned by the server, projected into Python-native types.

    ``value`` is the natural Python representation of whichever oneof variant
    the server returned (see the module docstring for the full table). Use
    ``kind`` if you need to dispatch on the wire type without ``isinstance``.

    ``expiration`` is the absolute expiration time, or ``None`` if the server
    response carried no expiration field.
    """

    key: str
    value: Any
    kind: VertexKind
    expiration: _dt.datetime | None = None


@dataclass(frozen=True, slots=True)
class Edge:
    """An edge returned by the server.

    Weights are ``float`` (the proto wire type is ``float32``); the
    Python-side widening to ``float`` is unconditional and lossless.
    """

    tail: str
    head: str
    weight: float
    expiration: _dt.datetime | None = None


@dataclass(slots=True)
class VertexInput:
    """One vertex for the batch ``put_vertices`` API."""

    key: str
    value: Any
    ttl_seconds: float | None = None
    expiration: _dt.datetime | None = None


@dataclass(slots=True)
class EdgeInput:
    """One edge for the batch ``add_edges`` / ``put_edges`` APIs."""

    tail: str
    head: str
    weight: float
    ttl_seconds: float | None = None
    expiration: _dt.datetime | None = None


@dataclass(slots=True)
class Graph:
    """SDK-native projection of an ``Illuminate`` response.

    The shape mirrors Go's ``client.Graph`` (``vertices: map[str, Vertex]``,
    ``edges: map[str, map[str, float]]``) so JSON dumps from either SDK are
    interchangeable.
    """

    vertices: dict[str, Vertex] = field(default_factory=dict)
    edges: dict[str, dict[str, float]] = field(default_factory=dict)


# ----------------------------------------------------------------------------
# Conversion: Python value → proto Vertex
# ----------------------------------------------------------------------------


def _expiration_pb(ttl_seconds: float | None, expiration: _dt.datetime | None) -> _Timestamp | None:
    """Resolve TTL/expiration into a Timestamp message (or None for unset)."""
    if expiration is not None and ttl_seconds is not None:
        raise ValueError("specify either ttl_seconds or expiration, not both")
    if expiration is not None:
        ts = _Timestamp()
        ts.FromDatetime(expiration)
        return ts
    if ttl_seconds is not None:
        when = _dt.datetime.now(tz=_dt.timezone.utc) + _dt.timedelta(seconds=ttl_seconds)
        ts = _Timestamp()
        ts.FromDatetime(when)
        return ts
    return None


def to_pb_vertex(
    key: str,
    value: Any,
    *,
    ttl_seconds: float | None = None,
    expiration: _dt.datetime | None = None,
) -> _pb.Vertex:
    """Encode a Python value as a protobuf ``Vertex``."""
    pv = _pb.Vertex(key=key)
    exp = _expiration_pb(ttl_seconds, expiration)
    if exp is not None:
        pv.expiration.CopyFrom(exp)

    if value is None:
        pv.nil = True
        return pv

    if isinstance(value, int32):
        pv.int32 = value.value
    elif isinstance(value, uint32):
        pv.uint32 = value.value
    elif isinstance(value, uint64):
        pv.uint64 = value.value
    elif isinstance(value, float32):
        pv.float32 = value.value
    elif isinstance(value, bool):
        # bool MUST come before int (bool is a subclass of int in Python).
        pv.bool = value
    elif isinstance(value, int):
        if value < 0:
            if value < -(2**63):
                raise _OverflowError(f"int {value} underflows int64")
            pv.int64 = value
        elif value >= 2**63:
            if value >= 2**64:
                raise _OverflowError(f"int {value} overflows uint64")
            pv.uint64 = value
        else:
            pv.int64 = value
    elif isinstance(value, float):
        pv.float64 = value
    elif isinstance(value, str):
        pv.string = value
    elif isinstance(value, (bytes, bytearray)):
        pv.bytes = bytes(value)
    elif isinstance(value, _dt.datetime):
        ts = _Timestamp()
        ts.FromDatetime(value)
        pv.timestamp.CopyFrom(ts)
    elif isinstance(value, _dt.timedelta):
        dur = _Duration()
        dur.FromTimedelta(value)
        pv.duration.CopyFrom(dur)
    else:
        raise TypeError(
            f"unsupported Vertex value type: {type(value).__name__}; "
            "supported: int, float, bool, str, bytes, datetime, timedelta, None, "
            "or one of int32/uint32/uint64/float32"
        )
    return pv


# ----------------------------------------------------------------------------
# Conversion: proto Vertex → Python Vertex
# ----------------------------------------------------------------------------


_KIND_BY_FIELD: dict[str, VertexKind] = {
    "float32": VertexKind.FLOAT32,
    "float64": VertexKind.FLOAT64,
    "int32": VertexKind.INT32,
    "int64": VertexKind.INT64,
    "uint32": VertexKind.UINT32,
    "uint64": VertexKind.UINT64,
    "bool": VertexKind.BOOL,
    "string": VertexKind.STRING,
    "bytes": VertexKind.BYTES,
    "timestamp": VertexKind.TIMESTAMP,
    "duration": VertexKind.DURATION,
    "nil": VertexKind.NIL,
}


def from_pb_vertex(pv: _pb.Vertex) -> Vertex:
    """Decode a protobuf ``Vertex`` into the SDK's :class:`Vertex` dataclass."""
    which = pv.WhichOneof("value")
    kind = _KIND_BY_FIELD.get(which or "", VertexKind.UNSET)
    value: Any
    if which is None:
        value = None
    elif which == "nil":
        value = None
    elif which == "timestamp":
        value = pv.timestamp.ToDatetime(tzinfo=_dt.timezone.utc)
    elif which == "duration":
        value = pv.duration.ToTimedelta()
    elif which == "bytes":
        value = bytes(pv.bytes)
    else:
        value = getattr(pv, which)
    expiration: _dt.datetime | None = None
    if pv.HasField("expiration"):
        expiration = pv.expiration.ToDatetime(tzinfo=_dt.timezone.utc)
        if expiration.timestamp() == 0:
            expiration = None
    return Vertex(key=pv.key, value=value, kind=kind, expiration=expiration)


def from_pb_edge(pe: _pb.Edge) -> Edge:
    """Decode a protobuf ``Edge`` into the SDK's :class:`Edge` dataclass."""
    expiration: _dt.datetime | None = None
    if pe.HasField("expiration"):
        expiration = pe.expiration.ToDatetime(tzinfo=_dt.timezone.utc)
        if expiration.timestamp() == 0:
            expiration = None
    return Edge(tail=pe.tail, head=pe.head, weight=pe.weight, expiration=expiration)


def _edge_input_to_pb(ei: EdgeInput) -> _pb.Edge:
    pe = _pb.Edge(tail=ei.tail, head=ei.head, weight=ei.weight)
    exp = _expiration_pb(ei.ttl_seconds, ei.expiration)
    if exp is not None:
        pe.expiration.CopyFrom(exp)
    return pe


def _vertex_input_to_pb(vi: VertexInput) -> _pb.Vertex:
    return to_pb_vertex(vi.key, vi.value, ttl_seconds=vi.ttl_seconds, expiration=vi.expiration)


__all__ = [
    "Edge",
    "EdgeInput",
    "Graph",
    "Optimization",
    "Vertex",
    "VertexInput",
    "VertexKind",
    "float32",
    "from_pb_edge",
    "from_pb_vertex",
    "int32",
    "to_pb_vertex",
    "uint32",
    "uint64",
]
