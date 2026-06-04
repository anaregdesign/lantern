"""Synchronous client for the Lantern gRPC service.

Open with :meth:`Lantern.connect` (single target / DNS) or
:meth:`Lantern.connect_endpoints` (explicit static endpoint list). The
returned :class:`Lantern` is a context manager — wrap it in ``with`` to
guarantee channel cleanup.

All methods translate :class:`grpc.RpcError` into the typed
:mod:`lantern_client.errors` subclasses; batch helpers (``put_vertices``,
``put_edges``, ``add_edges``, ``delete_vertices``, ``delete_edges``) auto-chunk
at the configured batch size and raise :class:`~lantern_client.BatchError`
with a resumable ``written`` offset on partial failure.
"""

from __future__ import annotations

import datetime as _dt
import json
from collections.abc import Iterator, Sequence

import grpc
from grpc_health.v1 import health_pb2, health_pb2_grpc

from ._pb.graph.v1 import graph_pb2 as _pb
from ._pb.graph.v1 import graph_pb2_grpc as _grpc
from .endpoints import has_endpoints, static_target
from .errors import BatchError, _wrap_rpc_error
from .options import (
    ConnectOptions,
    DeleteByPrefixOptions,
    EdgeScanOptions,
    IlluminateOptions,
    ScanOptions,
)
from .values import (
    Edge,
    EdgeInput,
    Graph,
    Optimization,
    Vertex,
    VertexInput,
    _edge_input_to_pb,
    _expiration_pb,
    _vertex_input_to_pb,
    from_pb_edge,
    from_pb_vertex,
    to_pb_vertex,
)

_DEFAULT_SERVICE_CONFIG = json.dumps(
    {
        "loadBalancingConfig": [{"round_robin": {}}],
        "methodConfig": [
            {
                "name": [{"service": "graph.v1.LanternService"}],
                "retryPolicy": {
                    "maxAttempts": 5,
                    "initialBackoff": "0.1s",
                    "maxBackoff": "2s",
                    "backoffMultiplier": 2.0,
                    "retryableStatusCodes": ["UNAVAILABLE", "RESOURCE_EXHAUSTED"],
                },
            },
            {
                # AddEdge / AddEdges are additive: omitting retryPolicy
                # disables retries entirely so duplicate weights cannot
                # accumulate from transient network blips.
                "name": [
                    {"service": "graph.v1.LanternService", "method": "AddEdge"},
                    {"service": "graph.v1.LanternService", "method": "AddEdges"},
                ],
            },
        ],
    }
)


def default_service_config() -> str:
    """Return the JSON service config the SDK applies by default.

    Exposed so callers can extend it (parse, merge their own ``methodConfig``,
    re-serialise) and pass the result via ``ConnectOptions.service_config_json``.
    """
    return _DEFAULT_SERVICE_CONFIG


class Lantern:
    """Synchronous gRPC client for the Lantern service.

    Construct via the classmethods :meth:`connect` /
    :meth:`connect_endpoints` rather than calling ``__init__`` directly.
    """

    __slots__ = ("_channel", "_stub", "_health", "_opts", "_metadata", "_closed")

    def __init__(
        self,
        channel: grpc.Channel,
        opts: ConnectOptions,
    ) -> None:
        self._channel = channel
        self._stub = _grpc.LanternServiceStub(channel)
        self._health = health_pb2_grpc.HealthStub(channel)
        self._opts = opts
        self._metadata: tuple[tuple[str, str], ...] = ()
        self._closed = False

    # ------------------------------------------------------------------
    # Connection lifecycle
    # ------------------------------------------------------------------

    @classmethod
    def connect(
        cls,
        target: str,
        *,
        credentials: grpc.ChannelCredentials | None = None,
        options: ConnectOptions | None = None,
        channel_options: Sequence[tuple[str, object]] | None = None,
    ) -> Lantern:
        """Open a client against a single target or DNS-resolved fan-out.

        ``target`` accepts the standard gRPC URI forms: ``"host:port"``,
        ``"dns:///host:port"``, ``"unix:/path/to/sock"``, etc.

        Pass ``credentials`` for mTLS or other auth; omit for an insecure
        channel. ``channel_options`` is forwarded verbatim to gRPC.
        """
        opts = options or ConnectOptions()
        ch_opts = list(channel_options or ())
        sc = opts.service_config_json or _DEFAULT_SERVICE_CONFIG
        ch_opts.append(("grpc.service_config", sc))
        if opts.user_agent:
            ch_opts.append(("grpc.primary_user_agent", opts.user_agent))
        if credentials is None:
            channel = grpc.insecure_channel(target, options=ch_opts)
        else:
            channel = grpc.secure_channel(target, credentials, options=ch_opts)
        return cls(channel, opts)

    @classmethod
    def connect_endpoints(
        cls,
        endpoints: Sequence[str],
        *,
        credentials: grpc.ChannelCredentials | None = None,
        options: ConnectOptions | None = None,
        channel_options: Sequence[tuple[str, object]] | None = None,
    ) -> Lantern:
        """Open a client that fans out across an explicit static endpoint list.

        Uses gRPC's ``ipv4:`` name resolver with ``round_robin`` LB so failed
        sub-channels rotate to the next endpoint automatically.
        """
        return cls.connect(
            static_target(endpoints),
            credentials=credentials,
            options=options,
            channel_options=channel_options,
        )

    def close(self) -> None:
        """Close the underlying channel. Idempotent."""
        if not self._closed:
            self._channel.close()
            self._closed = True

    def __enter__(self) -> Lantern:
        return self

    def __exit__(self, exc_type, exc, tb) -> None:  # noqa: ANN001
        self.close()

    @property
    def channel(self) -> grpc.Channel:
        """Underlying gRPC channel, exposed for advanced interop."""
        return self._channel

    # ------------------------------------------------------------------
    # Health
    # ------------------------------------------------------------------

    def ping(self, *, timeout_seconds: float | None = None) -> str:
        """Issue a ``grpc.health.v1.Health/Check`` and return the status name.

        Returns one of ``"SERVING"``, ``"NOT_SERVING"``, ``"UNKNOWN"``,
        ``"SERVICE_UNKNOWN"``. Raises :class:`~lantern_client.errors.LanternError`
        on transport failure.
        """
        req = health_pb2.HealthCheckRequest(service="graph.v1.LanternService")
        try:
            resp = self._health.Check(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return health_pb2.HealthCheckResponse.ServingStatus.Name(resp.status)

    # ------------------------------------------------------------------
    # Vertex — single
    # ------------------------------------------------------------------

    def get_vertex(self, key: str, *, timeout_seconds: float | None = None) -> Vertex:
        """Read one vertex. Raises :class:`NotFoundError` if absent."""
        req = _pb.GetVertexRequest(key=key)
        try:
            resp = self._stub.GetVertex(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return from_pb_vertex(resp.vertex)

    def put_vertex(
        self,
        key: str,
        value: object,
        *,
        ttl_seconds: float | None = None,
        expiration: _dt.datetime | None = None,
        timeout_seconds: float | None = None,
    ) -> None:
        """Write one vertex. Idempotent. See :mod:`lantern_client.values` for the type bridge."""
        pv = to_pb_vertex(key, value, ttl_seconds=ttl_seconds, expiration=expiration)
        req = _pb.PutVertexRequest(vertex=pv)
        try:
            self._stub.PutVertex(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e

    def delete_vertex(
        self,
        key: str,
        *,
        timeout_seconds: float | None = None,
    ) -> bool:
        """Delete one vertex. Returns ``True`` if the key existed, ``False`` otherwise."""
        req = _pb.DeleteVertexRequest(key=key)
        try:
            resp = self._stub.DeleteVertex(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return bool(resp.existed)

    # ------------------------------------------------------------------
    # Vertex — batch
    # ------------------------------------------------------------------

    def get_vertices(
        self,
        keys: Sequence[str],
        *,
        timeout_seconds: float | None = None,
    ) -> tuple[list[Vertex], list[str]]:
        """Read many vertices. Returns ``(present, missing_keys)``.

        Order of ``present`` is server-defined; match by ``Vertex.key``.
        """
        req = _pb.GetVerticesRequest(keys=list(keys))
        try:
            resp = self._stub.GetVertices(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return [from_pb_vertex(v) for v in resp.vertices], list(resp.missing)

    def put_vertices(
        self,
        inputs: Sequence[VertexInput],
        *,
        chunk_size: int | None = None,
        timeout_seconds: float | None = None,
    ) -> int:
        """Write many vertices, auto-chunked at :attr:`ConnectOptions.batch_chunk_size`.

        Returns the total written count. Raises :class:`BatchError` on partial
        failure, with ``err.written`` = count from prior successful chunks.
        """
        chunk = chunk_size or self._opts.batch_chunk_size
        written = 0
        for batch in _chunks(inputs, chunk):
            req = _pb.PutVerticesRequest(vertices=[_vertex_input_to_pb(vi) for vi in batch])
            try:
                resp = self._stub.PutVertices(req, timeout=self._resolve_timeout(timeout_seconds))
            except grpc.RpcError as e:
                raise BatchError(written, _wrap_rpc_error(e)) from e
            written += int(resp.written)
        return written

    def delete_vertices(
        self,
        keys: Sequence[str],
        *,
        chunk_size: int | None = None,
        timeout_seconds: float | None = None,
    ) -> int:
        """Delete many vertices. Returns total deleted count."""
        chunk = chunk_size or self._opts.batch_chunk_size
        deleted = 0
        seen = 0
        for batch in _chunks(keys, chunk):
            req = _pb.DeleteVerticesRequest(keys=list(batch))
            try:
                resp = self._stub.DeleteVertices(
                    req, timeout=self._resolve_timeout(timeout_seconds)
                )
            except grpc.RpcError as e:
                raise BatchError(seen, _wrap_rpc_error(e)) from e
            deleted += int(resp.deleted)
            seen += len(batch)
        return deleted

    # ------------------------------------------------------------------
    # Vertex — prefix
    # ------------------------------------------------------------------

    def scan_vertices(
        self,
        prefix: str,
        *,
        options: ScanOptions | None = None,
        timeout_seconds: float | None = None,
    ) -> tuple[list[Vertex], bytes]:
        """Page through vertices matching ``prefix``. Returns ``(batch, next_cursor)``.

        ``next_cursor == b""`` means the scan is exhausted.
        """
        opts = options or ScanOptions()
        req = _pb.ScanVerticesRequest(prefix=prefix, limit=opts.limit, cursor=opts.cursor)
        try:
            resp = self._stub.ScanVertices(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return [from_pb_vertex(v) for v in resp.vertices], bytes(resp.next_cursor)

    def scan_vertices_all(
        self,
        prefix: str,
        *,
        limit: int = 0,
        timeout_seconds: float | None = None,
    ) -> Iterator[Vertex]:
        """Yield every vertex matching ``prefix`` across all pages.

        Convenience wrapper around :meth:`scan_vertices` that loops until the
        cursor is exhausted.
        """
        cursor = b""
        while True:
            page, cursor = self.scan_vertices(
                prefix,
                options=ScanOptions(limit=limit, cursor=cursor),
                timeout_seconds=timeout_seconds,
            )
            yield from page
            if not cursor:
                return

    def count_vertices_by_prefix(
        self,
        prefix: str,
        *,
        timeout_seconds: float | None = None,
    ) -> int:
        """Count vertices whose key starts with ``prefix``."""
        req = _pb.CountVerticesByPrefixRequest(prefix=prefix)
        try:
            resp = self._stub.CountVerticesByPrefix(
                req, timeout=self._resolve_timeout(timeout_seconds)
            )
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return int(resp.count)

    def delete_vertices_by_prefix(
        self,
        prefix: str,
        *,
        options: DeleteByPrefixOptions | None = None,
        timeout_seconds: float | None = None,
    ) -> int:
        """Delete vertices matching ``prefix``. Returns the deleted count.

        Pass ``options=DeleteByPrefixOptions(dry_run=True)`` to preview without
        deleting.
        """
        opts = options or DeleteByPrefixOptions()
        req = _pb.DeleteVerticesByPrefixRequest(
            prefix=prefix, limit=opts.limit, dry_run=opts.dry_run
        )
        try:
            resp = self._stub.DeleteVerticesByPrefix(
                req, timeout=self._resolve_timeout(timeout_seconds)
            )
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return int(resp.deleted)

    # ------------------------------------------------------------------
    # Edge — single
    # ------------------------------------------------------------------

    def get_edge(
        self,
        tail: str,
        head: str,
        *,
        timeout_seconds: float | None = None,
    ) -> Edge:
        """Read one edge. Raises :class:`NotFoundError` if absent."""
        req = _pb.GetEdgeRequest(tail=tail, head=head)
        try:
            resp = self._stub.GetEdge(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return from_pb_edge(resp.edge)

    def add_edge(
        self,
        tail: str,
        head: str,
        weight: float = 1.0,
        *,
        ttl_seconds: float | None = None,
        expiration: _dt.datetime | None = None,
        timeout_seconds: float | None = None,
    ) -> None:
        """Add ``weight`` to the edge ``tail → head``. NOT idempotent — retries double-count."""
        pe = _pb.Edge(tail=tail, head=head, weight=weight)
        exp = _expiration_pb(ttl_seconds, expiration)
        if exp is not None:
            pe.expiration.CopyFrom(exp)
        req = _pb.AddEdgeRequest(edge=pe)
        try:
            self._stub.AddEdge(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e

    def put_edge(
        self,
        tail: str,
        head: str,
        weight: float = 1.0,
        *,
        ttl_seconds: float | None = None,
        expiration: _dt.datetime | None = None,
        timeout_seconds: float | None = None,
    ) -> None:
        """Overwrite the edge ``tail → head`` with ``weight``. Idempotent."""
        pe = _pb.Edge(tail=tail, head=head, weight=weight)
        exp = _expiration_pb(ttl_seconds, expiration)
        if exp is not None:
            pe.expiration.CopyFrom(exp)
        req = _pb.PutEdgeRequest(edge=pe)
        try:
            self._stub.PutEdge(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e

    def delete_edge(
        self,
        tail: str,
        head: str,
        *,
        timeout_seconds: float | None = None,
    ) -> bool:
        """Delete one edge. Returns ``True`` if it existed, ``False`` otherwise."""
        req = _pb.DeleteEdgeRequest(tail=tail, head=head)
        try:
            resp = self._stub.DeleteEdge(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return bool(resp.existed)

    # ------------------------------------------------------------------
    # Edge — batch
    # ------------------------------------------------------------------

    def get_edges(
        self,
        edges: Sequence[tuple[str, str]],
        *,
        timeout_seconds: float | None = None,
    ) -> tuple[list[Edge], list[tuple[str, str]]]:
        """Read many edges. Returns ``(present, missing_pairs)``."""
        req = _pb.GetEdgesRequest(edges=[_pb.EdgeKey(tail=t, head=h) for (t, h) in edges])
        try:
            resp = self._stub.GetEdges(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return (
            [from_pb_edge(e) for e in resp.edges],
            [(k.tail, k.head) for k in resp.missing],
        )

    def add_edges(
        self,
        inputs: Sequence[EdgeInput],
        *,
        chunk_size: int | None = None,
        timeout_seconds: float | None = None,
    ) -> int:
        """Add many edges in batch. NOT idempotent (retries double-count)."""
        chunk = chunk_size or self._opts.batch_chunk_size
        written = 0
        for batch in _chunks(inputs, chunk):
            req = _pb.AddEdgesRequest(edges=[_edge_input_to_pb(ei) for ei in batch])
            try:
                resp = self._stub.AddEdges(req, timeout=self._resolve_timeout(timeout_seconds))
            except grpc.RpcError as e:
                raise BatchError(written, _wrap_rpc_error(e)) from e
            written += int(resp.written)
        return written

    def put_edges(
        self,
        inputs: Sequence[EdgeInput],
        *,
        chunk_size: int | None = None,
        timeout_seconds: float | None = None,
    ) -> int:
        """Overwrite many edges. Idempotent — safe to retry full batch from 0."""
        chunk = chunk_size or self._opts.batch_chunk_size
        written = 0
        for batch in _chunks(inputs, chunk):
            req = _pb.PutEdgesRequest(edges=[_edge_input_to_pb(ei) for ei in batch])
            try:
                resp = self._stub.PutEdges(req, timeout=self._resolve_timeout(timeout_seconds))
            except grpc.RpcError as e:
                raise BatchError(written, _wrap_rpc_error(e)) from e
            written += int(resp.written)
        return written

    def delete_edges(
        self,
        edges: Sequence[tuple[str, str]],
        *,
        chunk_size: int | None = None,
        timeout_seconds: float | None = None,
    ) -> int:
        """Delete many edges. Returns total deleted count."""
        chunk = chunk_size or self._opts.batch_chunk_size
        deleted = 0
        seen = 0
        for batch in _chunks(edges, chunk):
            req = _pb.DeleteEdgesRequest(edges=[_pb.EdgeKey(tail=t, head=h) for (t, h) in batch])
            try:
                resp = self._stub.DeleteEdges(req, timeout=self._resolve_timeout(timeout_seconds))
            except grpc.RpcError as e:
                raise BatchError(seen, _wrap_rpc_error(e)) from e
            deleted += int(resp.deleted)
            seen += len(batch)
        return deleted

    # ------------------------------------------------------------------
    # Edge — prefix
    # ------------------------------------------------------------------

    def scan_edges(
        self,
        *,
        options: EdgeScanOptions | None = None,
        timeout_seconds: float | None = None,
    ) -> tuple[list[Edge], bytes]:
        """Page through edges filtered by tail/head prefix. Returns ``(batch, next_cursor)``."""
        opts = options or EdgeScanOptions()
        req = _pb.ScanEdgesRequest(
            tail_prefix=opts.tail_prefix,
            head_prefix=opts.head_prefix,
            limit=opts.limit,
            cursor=opts.cursor,
        )
        try:
            resp = self._stub.ScanEdges(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        return [from_pb_edge(e) for e in resp.edges], bytes(resp.next_cursor)

    def scan_edges_all(
        self,
        *,
        tail_prefix: str = "",
        head_prefix: str = "",
        limit: int = 0,
        timeout_seconds: float | None = None,
    ) -> Iterator[Edge]:
        """Yield every edge matching the prefix filters across all pages."""
        cursor = b""
        while True:
            page, cursor = self.scan_edges(
                options=EdgeScanOptions(
                    tail_prefix=tail_prefix,
                    head_prefix=head_prefix,
                    limit=limit,
                    cursor=cursor,
                ),
                timeout_seconds=timeout_seconds,
            )
            yield from page
            if not cursor:
                return

    # ------------------------------------------------------------------
    # Illuminate
    # ------------------------------------------------------------------

    def illuminate(
        self,
        seed: str,
        *,
        step: int = 0,
        k: int = 0,
        tfidf: bool = False,
        optimization: Optimization = Optimization.UNSPECIFIED,
        options: IlluminateOptions | None = None,
        timeout_seconds: float | None = None,
    ) -> Graph:
        """Run k-bounded BFS from ``seed`` and return the subgraph.

        Either pass the inline kwargs or an :class:`IlluminateOptions`; mixing
        the two means the options object wins for any field it sets.
        """
        opts = options or IlluminateOptions(step=step, k=k, tfidf=tfidf, optimization=optimization)
        req = _pb.IlluminateRequest(
            seed=seed,
            step=opts.step,
            k=opts.k,
            tfidf=opts.tfidf,
            optimization=int(opts.optimization),
        )
        try:
            resp = self._stub.Illuminate(req, timeout=self._resolve_timeout(timeout_seconds))
        except grpc.RpcError as e:
            raise _wrap_rpc_error(e) from e
        graph = Graph()
        for v in resp.graph.vertices:
            graph.vertices[v.key] = from_pb_vertex(v)
        for e in resp.graph.edges:
            graph.edges.setdefault(e.tail, {})[e.head] = e.weight
        return graph

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    def _resolve_timeout(self, per_call: float | None) -> float | None:
        if per_call is not None:
            return per_call
        return self._opts.default_timeout_seconds


def _chunks(seq: Sequence, n: int) -> Iterator[Sequence]:
    if n <= 0:
        yield seq
        return
    for i in range(0, len(seq), n):
        yield seq[i : i + n]


# Re-export to keep imports reachable through this module path.
__all__ = ["Lantern", "default_service_config", "has_endpoints"]
