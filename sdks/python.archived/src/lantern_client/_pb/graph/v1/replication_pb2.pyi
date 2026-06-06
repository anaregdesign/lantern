import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from graph.v1 import graph_pb2 as _graph_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class HLCTimestamp(_message.Message):
    __slots__ = ("wall_ns", "logical", "node_id")
    WALL_NS_FIELD_NUMBER: _ClassVar[int]
    LOGICAL_FIELD_NUMBER: _ClassVar[int]
    NODE_ID_FIELD_NUMBER: _ClassVar[int]
    wall_ns: int
    logical: int
    node_id: bytes
    def __init__(self, wall_ns: _Optional[int] = ..., logical: _Optional[int] = ..., node_id: _Optional[bytes] = ...) -> None: ...

class MutationOp(_message.Message):
    __slots__ = ("put_vertex", "put_vertices", "delete_vertex", "delete_vertices", "delete_vertices_by_prefix", "add_edge", "add_edges", "put_edge", "put_edges", "delete_edge", "delete_edges")
    PUT_VERTEX_FIELD_NUMBER: _ClassVar[int]
    PUT_VERTICES_FIELD_NUMBER: _ClassVar[int]
    DELETE_VERTEX_FIELD_NUMBER: _ClassVar[int]
    DELETE_VERTICES_FIELD_NUMBER: _ClassVar[int]
    DELETE_VERTICES_BY_PREFIX_FIELD_NUMBER: _ClassVar[int]
    ADD_EDGE_FIELD_NUMBER: _ClassVar[int]
    ADD_EDGES_FIELD_NUMBER: _ClassVar[int]
    PUT_EDGE_FIELD_NUMBER: _ClassVar[int]
    PUT_EDGES_FIELD_NUMBER: _ClassVar[int]
    DELETE_EDGE_FIELD_NUMBER: _ClassVar[int]
    DELETE_EDGES_FIELD_NUMBER: _ClassVar[int]
    put_vertex: _graph_pb2.PutVertexRequest
    put_vertices: _graph_pb2.PutVerticesRequest
    delete_vertex: _graph_pb2.DeleteVertexRequest
    delete_vertices: _graph_pb2.DeleteVerticesRequest
    delete_vertices_by_prefix: _graph_pb2.DeleteVerticesByPrefixRequest
    add_edge: _graph_pb2.AddEdgeRequest
    add_edges: _graph_pb2.AddEdgesRequest
    put_edge: _graph_pb2.PutEdgeRequest
    put_edges: _graph_pb2.PutEdgesRequest
    delete_edge: _graph_pb2.DeleteEdgeRequest
    delete_edges: _graph_pb2.DeleteEdgesRequest
    def __init__(self, put_vertex: _Optional[_Union[_graph_pb2.PutVertexRequest, _Mapping]] = ..., put_vertices: _Optional[_Union[_graph_pb2.PutVerticesRequest, _Mapping]] = ..., delete_vertex: _Optional[_Union[_graph_pb2.DeleteVertexRequest, _Mapping]] = ..., delete_vertices: _Optional[_Union[_graph_pb2.DeleteVerticesRequest, _Mapping]] = ..., delete_vertices_by_prefix: _Optional[_Union[_graph_pb2.DeleteVerticesByPrefixRequest, _Mapping]] = ..., add_edge: _Optional[_Union[_graph_pb2.AddEdgeRequest, _Mapping]] = ..., add_edges: _Optional[_Union[_graph_pb2.AddEdgesRequest, _Mapping]] = ..., put_edge: _Optional[_Union[_graph_pb2.PutEdgeRequest, _Mapping]] = ..., put_edges: _Optional[_Union[_graph_pb2.PutEdgesRequest, _Mapping]] = ..., delete_edge: _Optional[_Union[_graph_pb2.DeleteEdgeRequest, _Mapping]] = ..., delete_edges: _Optional[_Union[_graph_pb2.DeleteEdgesRequest, _Mapping]] = ...) -> None: ...

class Mutation(_message.Message):
    __slots__ = ("seq", "hlc", "origin", "op")
    SEQ_FIELD_NUMBER: _ClassVar[int]
    HLC_FIELD_NUMBER: _ClassVar[int]
    ORIGIN_FIELD_NUMBER: _ClassVar[int]
    OP_FIELD_NUMBER: _ClassVar[int]
    seq: int
    hlc: HLCTimestamp
    origin: bytes
    op: MutationOp
    def __init__(self, seq: _Optional[int] = ..., hlc: _Optional[_Union[HLCTimestamp, _Mapping]] = ..., origin: _Optional[bytes] = ..., op: _Optional[_Union[MutationOp, _Mapping]] = ...) -> None: ...

class SubscribeRequest(_message.Message):
    __slots__ = ("from_seq",)
    FROM_SEQ_FIELD_NUMBER: _ClassVar[int]
    from_seq: int
    def __init__(self, from_seq: _Optional[int] = ...) -> None: ...

class SubscribeResponse(_message.Message):
    __slots__ = ("mutation",)
    MUTATION_FIELD_NUMBER: _ClassVar[int]
    mutation: Mutation
    def __init__(self, mutation: _Optional[_Union[Mutation, _Mapping]] = ...) -> None: ...

class SnapshotRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class SnapshotHeader(_message.Message):
    __slots__ = ("cutoff_seq", "cutoff_hlc")
    CUTOFF_SEQ_FIELD_NUMBER: _ClassVar[int]
    CUTOFF_HLC_FIELD_NUMBER: _ClassVar[int]
    cutoff_seq: int
    cutoff_hlc: HLCTimestamp
    def __init__(self, cutoff_seq: _Optional[int] = ..., cutoff_hlc: _Optional[_Union[HLCTimestamp, _Mapping]] = ...) -> None: ...

class SnapshotFooter(_message.Message):
    __slots__ = ("vertex_count", "edge_count")
    VERTEX_COUNT_FIELD_NUMBER: _ClassVar[int]
    EDGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    vertex_count: int
    edge_count: int
    def __init__(self, vertex_count: _Optional[int] = ..., edge_count: _Optional[int] = ...) -> None: ...

class SnapshotVertex(_message.Message):
    __slots__ = ("vertex", "hlc")
    VERTEX_FIELD_NUMBER: _ClassVar[int]
    HLC_FIELD_NUMBER: _ClassVar[int]
    vertex: _graph_pb2.Vertex
    hlc: HLCTimestamp
    def __init__(self, vertex: _Optional[_Union[_graph_pb2.Vertex, _Mapping]] = ..., hlc: _Optional[_Union[HLCTimestamp, _Mapping]] = ...) -> None: ...

class SnapshotEdgeContribution(_message.Message):
    __slots__ = ("weight", "expiration", "contrib_id")
    WEIGHT_FIELD_NUMBER: _ClassVar[int]
    EXPIRATION_FIELD_NUMBER: _ClassVar[int]
    CONTRIB_ID_FIELD_NUMBER: _ClassVar[int]
    weight: float
    expiration: _timestamp_pb2.Timestamp
    contrib_id: bytes
    def __init__(self, weight: _Optional[float] = ..., expiration: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., contrib_id: _Optional[bytes] = ...) -> None: ...

class SnapshotEdge(_message.Message):
    __slots__ = ("tail", "head", "hlc", "contributions")
    TAIL_FIELD_NUMBER: _ClassVar[int]
    HEAD_FIELD_NUMBER: _ClassVar[int]
    HLC_FIELD_NUMBER: _ClassVar[int]
    CONTRIBUTIONS_FIELD_NUMBER: _ClassVar[int]
    tail: str
    head: str
    hlc: HLCTimestamp
    contributions: _containers.RepeatedCompositeFieldContainer[SnapshotEdgeContribution]
    def __init__(self, tail: _Optional[str] = ..., head: _Optional[str] = ..., hlc: _Optional[_Union[HLCTimestamp, _Mapping]] = ..., contributions: _Optional[_Iterable[_Union[SnapshotEdgeContribution, _Mapping]]] = ...) -> None: ...

class SnapshotResponse(_message.Message):
    __slots__ = ("header", "vertex", "edge", "footer")
    HEADER_FIELD_NUMBER: _ClassVar[int]
    VERTEX_FIELD_NUMBER: _ClassVar[int]
    EDGE_FIELD_NUMBER: _ClassVar[int]
    FOOTER_FIELD_NUMBER: _ClassVar[int]
    header: SnapshotHeader
    vertex: SnapshotVertex
    edge: SnapshotEdge
    footer: SnapshotFooter
    def __init__(self, header: _Optional[_Union[SnapshotHeader, _Mapping]] = ..., vertex: _Optional[_Union[SnapshotVertex, _Mapping]] = ..., edge: _Optional[_Union[SnapshotEdge, _Mapping]] = ..., footer: _Optional[_Union[SnapshotFooter, _Mapping]] = ...) -> None: ...

class PeerStatusRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class OriginState(_message.Message):
    __slots__ = ("origin", "last_seq", "last_hlc")
    ORIGIN_FIELD_NUMBER: _ClassVar[int]
    LAST_SEQ_FIELD_NUMBER: _ClassVar[int]
    LAST_HLC_FIELD_NUMBER: _ClassVar[int]
    origin: bytes
    last_seq: int
    last_hlc: HLCTimestamp
    def __init__(self, origin: _Optional[bytes] = ..., last_seq: _Optional[int] = ..., last_hlc: _Optional[_Union[HLCTimestamp, _Mapping]] = ...) -> None: ...

class PeerStatusResponse(_message.Message):
    __slots__ = ("self_origin", "origins")
    SELF_ORIGIN_FIELD_NUMBER: _ClassVar[int]
    ORIGINS_FIELD_NUMBER: _ClassVar[int]
    self_origin: bytes
    origins: _containers.RepeatedCompositeFieldContainer[OriginState]
    def __init__(self, self_origin: _Optional[bytes] = ..., origins: _Optional[_Iterable[_Union[OriginState, _Mapping]]] = ...) -> None: ...
