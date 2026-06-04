"""Unit tests for the Go-native ↔ proto value bridge."""

from __future__ import annotations

import datetime as dt

import pytest

from lantern_client import (
    Edge,
    Vertex,
    VertexKind,
    float32,
    int32,
    uint32,
    uint64,
)
from lantern_client._pb.graph.v1 import graph_pb2 as _pb
from lantern_client.errors import OverflowError as LOverflowError
from lantern_client.values import from_pb_edge, from_pb_vertex, to_pb_vertex


def _roundtrip(key: str, value: object) -> Vertex:
    return from_pb_vertex(to_pb_vertex(key, value))


# ---------------------------------------------------------------------------
# Scalars
# ---------------------------------------------------------------------------


def test_bool_dispatches_before_int():
    v = _roundtrip("b", True)
    assert v.kind == VertexKind.BOOL
    assert v.value is True


def test_bool_false():
    v = _roundtrip("b", False)
    assert v.kind == VertexKind.BOOL
    assert v.value is False


def test_int_small_positive_maps_to_int64():
    v = _roundtrip("n", 42)
    assert v.kind == VertexKind.INT64
    assert v.value == 42


def test_int_negative_maps_to_int64():
    v = _roundtrip("n", -7)
    assert v.kind == VertexKind.INT64
    assert v.value == -7


def test_int_above_int64_max_uses_uint64():
    big = (1 << 63) + 5
    v = _roundtrip("n", big)
    assert v.kind == VertexKind.UINT64
    assert v.value == big


def test_int_overflow_raises():
    with pytest.raises(LOverflowError):
        to_pb_vertex("n", 1 << 65)


def test_int32_marker():
    v = _roundtrip("n", int32(7))
    assert v.kind == VertexKind.INT32
    assert v.value == 7


def test_int32_range():
    with pytest.raises(LOverflowError):
        int32(1 << 32)


def test_uint32_marker():
    v = _roundtrip("n", uint32(7))
    assert v.kind == VertexKind.UINT32


def test_uint64_marker():
    v = _roundtrip("n", uint64(7))
    assert v.kind == VertexKind.UINT64


def test_uint32_rejects_negative():
    with pytest.raises(LOverflowError):
        uint32(-1)


def test_float_maps_to_float64():
    v = _roundtrip("f", 1.5)
    assert v.kind == VertexKind.FLOAT64
    assert v.value == 1.5


def test_float32_marker():
    v = _roundtrip("f", float32(1.5))
    assert v.kind == VertexKind.FLOAT32


def test_string_roundtrip():
    v = _roundtrip("s", "hello")
    assert v.kind == VertexKind.STRING
    assert v.value == "hello"


def test_bytes_roundtrip():
    v = _roundtrip("b", b"\x00\x01\x02")
    assert v.kind == VertexKind.BYTES
    assert v.value == b"\x00\x01\x02"


def test_bytearray_input_decodes_as_bytes():
    v = _roundtrip("b", bytearray(b"abc"))
    assert v.kind == VertexKind.BYTES
    assert v.value == b"abc"


def test_none_maps_to_nil():
    v = _roundtrip("n", None)
    assert v.kind == VertexKind.NIL
    assert v.value is None


# ---------------------------------------------------------------------------
# Time types
# ---------------------------------------------------------------------------


def test_datetime_roundtrip_preserves_utc():
    src = dt.datetime(2025, 1, 2, 3, 4, 5, tzinfo=dt.timezone.utc)
    v = _roundtrip("t", src)
    assert v.kind == VertexKind.TIMESTAMP
    assert v.value == src


def test_timedelta_roundtrip():
    src = dt.timedelta(seconds=12, microseconds=500_000)
    v = _roundtrip("d", src)
    assert v.kind == VertexKind.DURATION
    assert v.value == src


# ---------------------------------------------------------------------------
# Expiration
# ---------------------------------------------------------------------------


def test_ttl_seconds_sets_expiration():
    pv = to_pb_vertex("k", 1, ttl_seconds=60)
    assert pv.HasField("expiration")


def test_expiration_datetime():
    when = dt.datetime(2030, 1, 1, tzinfo=dt.timezone.utc)
    pv = to_pb_vertex("k", 1, expiration=when)
    assert pv.expiration.ToDatetime(dt.timezone.utc) == when


def test_ttl_and_expiration_mutually_exclusive():
    with pytest.raises(ValueError):
        to_pb_vertex("k", 1, ttl_seconds=5, expiration=dt.datetime.now(dt.timezone.utc))


# ---------------------------------------------------------------------------
# Edge
# ---------------------------------------------------------------------------


def test_edge_roundtrip():
    pe = _pb.Edge(tail="a", head="b", weight=2.5)
    e = from_pb_edge(pe)
    assert isinstance(e, Edge)
    assert (e.tail, e.head, e.weight) == ("a", "b", pytest.approx(2.5))
