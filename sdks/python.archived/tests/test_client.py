"""End-to-end tests against an in-process gRPC ``LanternService`` fake.

The fake implements just enough of the service to round-trip vertex / edge /
illuminate / batch traffic and to exercise the error-translation layer. It
avoids spawning the real Go binary so the test suite can run under any CI
matrix that has Python.
"""

from __future__ import annotations

from collections.abc import Iterator
from concurrent import futures

import grpc
import pytest

from lantern_client import (
    BatchError,
    EdgeInput,
    Lantern,
    NotFoundError,
    Optimization,
    VertexInput,
)
from lantern_client._pb.graph.v1 import graph_pb2 as _pb
from lantern_client._pb.graph.v1 import graph_pb2_grpc as _grpc
from lantern_client.values import from_pb_vertex


class _FakeService(_grpc.LanternServiceServicer):
    def __init__(self) -> None:
        self.vertices: dict[str, _pb.Vertex] = {}
        self.edges: dict[tuple[str, str], _pb.Edge] = {}
        self.put_vertices_call_count = 0
        self.fail_put_vertices_on_call: int | None = None

    # vertex -----------------------------------------------------------------
    def GetVertex(self, request, context):  # noqa: N802
        if request.key not in self.vertices:
            context.abort(grpc.StatusCode.NOT_FOUND, f"{request.key} not found")
        return _pb.GetVertexResponse(vertex=self.vertices[request.key])

    def GetVertices(self, request, context):  # noqa: N802
        present, missing = [], []
        for k in request.keys:
            if k in self.vertices:
                present.append(self.vertices[k])
            else:
                missing.append(k)
        return _pb.GetVerticesResponse(vertices=present, missing=missing)

    def PutVertex(self, request, context):  # noqa: N802
        self.vertices[request.vertex.key] = request.vertex
        return _pb.PutVertexResponse()

    def PutVertices(self, request, context):  # noqa: N802
        self.put_vertices_call_count += 1
        if self.fail_put_vertices_on_call == self.put_vertices_call_count:
            context.abort(grpc.StatusCode.INTERNAL, "synthetic")
        for v in request.vertices:
            self.vertices[v.key] = v
        return _pb.PutVerticesResponse(written=len(request.vertices))

    def DeleteVertex(self, request, context):  # noqa: N802
        existed = self.vertices.pop(request.key, None) is not None
        return _pb.DeleteVertexResponse(existed=existed)

    def DeleteVertices(self, request, context):  # noqa: N802
        n = 0
        for k in request.keys:
            if self.vertices.pop(k, None) is not None:
                n += 1
        return _pb.DeleteVerticesResponse(deleted=n)

    def ScanVertices(self, request, context):  # noqa: N802
        hits = [v for k, v in self.vertices.items() if k.startswith(request.prefix)]
        return _pb.ScanVerticesResponse(vertices=hits, next_cursor=b"")

    def CountVerticesByPrefix(self, request, context):  # noqa: N802
        n = sum(1 for k in self.vertices if k.startswith(request.prefix))
        return _pb.CountVerticesByPrefixResponse(count=n)

    def DeleteVerticesByPrefix(self, request, context):  # noqa: N802
        keys = [k for k in self.vertices if k.startswith(request.prefix)]
        if not request.dry_run:
            for k in keys:
                del self.vertices[k]
        return _pb.DeleteVerticesByPrefixResponse(deleted=len(keys))

    # edge -------------------------------------------------------------------
    def GetEdge(self, request, context):  # noqa: N802
        e = self.edges.get((request.tail, request.head))
        if e is None:
            context.abort(grpc.StatusCode.NOT_FOUND, "no edge")
        return _pb.GetEdgeResponse(edge=e)

    def GetEdges(self, request, context):  # noqa: N802
        present, missing = [], []
        for k in request.edges:
            e = self.edges.get((k.tail, k.head))
            if e is None:
                missing.append(_pb.EdgeKey(tail=k.tail, head=k.head))
            else:
                present.append(e)
        return _pb.GetEdgesResponse(edges=present, missing=missing)

    def AddEdge(self, request, context):  # noqa: N802
        key = (request.edge.tail, request.edge.head)
        prev = self.edges.get(key)
        if prev is not None:
            e = _pb.Edge(
                tail=prev.tail,
                head=prev.head,
                weight=prev.weight + request.edge.weight,
            )
            self.edges[key] = e
        else:
            self.edges[key] = request.edge
        return _pb.AddEdgeResponse()

    def AddEdges(self, request, context):  # noqa: N802
        for e in request.edges:
            self.AddEdge(_pb.AddEdgeRequest(edge=e), context)
        return _pb.AddEdgesResponse(written=len(request.edges))

    def PutEdge(self, request, context):  # noqa: N802
        self.edges[(request.edge.tail, request.edge.head)] = request.edge
        return _pb.PutEdgeResponse()

    def PutEdges(self, request, context):  # noqa: N802
        for e in request.edges:
            self.edges[(e.tail, e.head)] = e
        return _pb.PutEdgesResponse(written=len(request.edges))

    def DeleteEdge(self, request, context):  # noqa: N802
        existed = self.edges.pop((request.tail, request.head), None) is not None
        return _pb.DeleteEdgeResponse(existed=existed)

    def DeleteEdges(self, request, context):  # noqa: N802
        n = 0
        for k in request.edges:
            if self.edges.pop((k.tail, k.head), None) is not None:
                n += 1
        return _pb.DeleteEdgesResponse(deleted=n)

    def ScanEdges(self, request, context):  # noqa: N802
        hits = [
            e
            for (t, h), e in self.edges.items()
            if t.startswith(request.tail_prefix) and h.startswith(request.head_prefix)
        ]
        return _pb.ScanEdgesResponse(edges=hits, next_cursor=b"")

    # illuminate -------------------------------------------------------------
    def Illuminate(self, request, context):  # noqa: N802
        g = _pb.Graph(
            vertices=list(self.vertices.values()),
            edges=list(self.edges.values()),
        )
        return _pb.IlluminateResponse(graph=g)


@pytest.fixture()
def server() -> Iterator[tuple[Lantern, _FakeService]]:
    fake = _FakeService()
    srv = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    _grpc.add_LanternServiceServicer_to_server(fake, srv)
    port = srv.add_insecure_port("127.0.0.1:0")
    srv.start()
    client = Lantern.connect(f"127.0.0.1:{port}")
    try:
        yield client, fake
    finally:
        client.close()
        srv.stop(grace=None)


def test_put_get_vertex(server):
    c, _ = server
    c.put_vertex("k", 42)
    v = c.get_vertex("k")
    assert v.value == 42


def test_get_vertex_not_found_raises(server):
    c, _ = server
    with pytest.raises(NotFoundError):
        c.get_vertex("missing")


def test_delete_vertex_returns_existed(server):
    c, _ = server
    c.put_vertex("k", 1)
    assert c.delete_vertex("k") is True
    assert c.delete_vertex("k") is False


def test_batch_put_vertices(server):
    c, _ = server
    n = c.put_vertices([VertexInput(key=f"k{i}", value=i) for i in range(5)])
    assert n == 5
    present, missing = c.get_vertices([f"k{i}" for i in range(7)])
    assert len(present) == 5
    assert sorted(missing) == ["k5", "k6"]


def test_batch_put_vertices_auto_chunks(server):
    c, _ = server
    inputs = [VertexInput(key=f"k{i}", value=i) for i in range(2500)]
    n = c.put_vertices(inputs, chunk_size=1000)
    assert n == 2500


def test_batch_partial_failure_raises_batch_error(server):
    c, fake = server
    inputs = [VertexInput(key=f"k{i}", value=i) for i in range(1500)]
    # Arm chunk #2 to fail; chunk #1 (k0..k999) should still be persisted.
    fake.fail_put_vertices_on_call = 2
    with pytest.raises(BatchError) as exc:
        c.put_vertices(inputs, chunk_size=1000)
    assert exc.value.written == 1000
    # The first chunk landed on the server.
    assert len(fake.vertices) == 1000


def test_count_by_prefix(server):
    c, _ = server
    for i in range(3):
        c.put_vertex(f"x:{i}", i)
    c.put_vertex("y:0", 0)
    assert c.count_vertices_by_prefix("x:") == 3
    assert c.count_vertices_by_prefix("y:") == 1


def test_scan_vertices_all(server):
    c, _ = server
    for i in range(5):
        c.put_vertex(f"p:{i}", i)
    out = list(c.scan_vertices_all("p:"))
    assert len(out) == 5


def test_edge_roundtrip(server):
    c, _ = server
    c.put_edge("a", "b", 2.5)
    e = c.get_edge("a", "b")
    assert e.weight == pytest.approx(2.5)


def test_add_edge_is_additive(server):
    c, _ = server
    c.add_edge("a", "b", 1.0)
    c.add_edge("a", "b", 2.0)
    e = c.get_edge("a", "b")
    assert e.weight == pytest.approx(3.0)


def test_get_edges_returns_missing(server):
    c, _ = server
    c.put_edge("a", "b", 1.0)
    present, missing = c.get_edges([("a", "b"), ("c", "d")])
    assert len(present) == 1
    assert missing == [("c", "d")]


def test_delete_edges_batch(server):
    c, _ = server
    c.put_edge("a", "b", 1.0)
    c.put_edge("a", "c", 1.0)
    assert c.delete_edges([("a", "b"), ("a", "c"), ("x", "y")]) == 2


def test_illuminate_returns_graph(server):
    c, _ = server
    c.put_vertex("a", 1)
    c.put_vertex("b", 2)
    c.put_edge("a", "b", 1.5)
    g = c.illuminate("a", step=1, optimization=Optimization.UNSPECIFIED)
    assert set(g.vertices) == {"a", "b"}
    assert g.edges["a"]["b"] == pytest.approx(1.5)


def test_context_manager_closes_channel():
    fake = _FakeService()
    srv = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
    _grpc.add_LanternServiceServicer_to_server(fake, srv)
    port = srv.add_insecure_port("127.0.0.1:0")
    srv.start()
    try:
        with Lantern.connect(f"127.0.0.1:{port}") as c:
            c.put_vertex("k", 1)
        # Channel should be closed; sending another RPC should raise.
        from lantern_client import LanternError

        with pytest.raises((LanternError, ValueError)):
            c.get_vertex("k")
    finally:
        srv.stop(grace=None)


def test_add_edges_batch(server):
    c, _ = server
    inputs = [EdgeInput(tail="a", head=f"h{i}", weight=1.0) for i in range(3)]
    n = c.add_edges(inputs)
    assert n == 3


# Suppress unused-import lint for from_pb_vertex (kept for symmetry).
_ = from_pb_vertex
