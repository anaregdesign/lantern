import datetime

from google.api import annotations_pb2 as _annotations_pb2
from google.protobuf import duration_pb2 as _duration_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Optimization(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    OPTIMIZATION_UNSPECIFIED: _ClassVar[Optimization]
    OPTIMIZATION_MINIMUM_SPANNING_TREE: _ClassVar[Optimization]
    OPTIMIZATION_MAXIMUM_SPANNING_TREE: _ClassVar[Optimization]
    OPTIMIZATION_SHORTEST_PATH_TREE: _ClassVar[Optimization]
    OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE: _ClassVar[Optimization]
OPTIMIZATION_UNSPECIFIED: Optimization
OPTIMIZATION_MINIMUM_SPANNING_TREE: Optimization
OPTIMIZATION_MAXIMUM_SPANNING_TREE: Optimization
OPTIMIZATION_SHORTEST_PATH_TREE: Optimization
OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE: Optimization

class Vertex(_message.Message):
    __slots__ = ("key", "expiration", "float64", "float32", "int32", "int64", "uint32", "uint64", "bool", "string", "bytes", "timestamp", "duration", "nil")
    KEY_FIELD_NUMBER: _ClassVar[int]
    EXPIRATION_FIELD_NUMBER: _ClassVar[int]
    FLOAT64_FIELD_NUMBER: _ClassVar[int]
    FLOAT32_FIELD_NUMBER: _ClassVar[int]
    INT32_FIELD_NUMBER: _ClassVar[int]
    INT64_FIELD_NUMBER: _ClassVar[int]
    UINT32_FIELD_NUMBER: _ClassVar[int]
    UINT64_FIELD_NUMBER: _ClassVar[int]
    BOOL_FIELD_NUMBER: _ClassVar[int]
    STRING_FIELD_NUMBER: _ClassVar[int]
    BYTES_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    DURATION_FIELD_NUMBER: _ClassVar[int]
    NIL_FIELD_NUMBER: _ClassVar[int]
    key: str
    expiration: _timestamp_pb2.Timestamp
    float64: float
    float32: float
    int32: int
    int64: int
    uint32: int
    uint64: int
    bool: bool
    string: str
    bytes: bytes
    timestamp: _timestamp_pb2.Timestamp
    duration: _duration_pb2.Duration
    nil: bool
    def __init__(self, key: _Optional[str] = ..., expiration: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., float64: _Optional[float] = ..., float32: _Optional[float] = ..., int32: _Optional[int] = ..., int64: _Optional[int] = ..., uint32: _Optional[int] = ..., uint64: _Optional[int] = ..., bool: _Optional[bool] = ..., string: _Optional[str] = ..., bytes: _Optional[bytes] = ..., timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., duration: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., nil: _Optional[bool] = ...) -> None: ...

class Edge(_message.Message):
    __slots__ = ("tail", "head", "weight", "expiration")
    TAIL_FIELD_NUMBER: _ClassVar[int]
    HEAD_FIELD_NUMBER: _ClassVar[int]
    WEIGHT_FIELD_NUMBER: _ClassVar[int]
    EXPIRATION_FIELD_NUMBER: _ClassVar[int]
    tail: str
    head: str
    weight: float
    expiration: _timestamp_pb2.Timestamp
    def __init__(self, tail: _Optional[str] = ..., head: _Optional[str] = ..., weight: _Optional[float] = ..., expiration: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class Graph(_message.Message):
    __slots__ = ("vertices", "edges")
    VERTICES_FIELD_NUMBER: _ClassVar[int]
    EDGES_FIELD_NUMBER: _ClassVar[int]
    vertices: _containers.RepeatedCompositeFieldContainer[Vertex]
    edges: _containers.RepeatedCompositeFieldContainer[Edge]
    def __init__(self, vertices: _Optional[_Iterable[_Union[Vertex, _Mapping]]] = ..., edges: _Optional[_Iterable[_Union[Edge, _Mapping]]] = ...) -> None: ...

class IlluminateRequest(_message.Message):
    __slots__ = ("seed", "step", "k", "tfidf", "optimization")
    SEED_FIELD_NUMBER: _ClassVar[int]
    STEP_FIELD_NUMBER: _ClassVar[int]
    K_FIELD_NUMBER: _ClassVar[int]
    TFIDF_FIELD_NUMBER: _ClassVar[int]
    OPTIMIZATION_FIELD_NUMBER: _ClassVar[int]
    seed: str
    step: int
    k: int
    tfidf: bool
    optimization: Optimization
    def __init__(self, seed: _Optional[str] = ..., step: _Optional[int] = ..., k: _Optional[int] = ..., tfidf: _Optional[bool] = ..., optimization: _Optional[_Union[Optimization, str]] = ...) -> None: ...

class IlluminateResponse(_message.Message):
    __slots__ = ("graph",)
    GRAPH_FIELD_NUMBER: _ClassVar[int]
    graph: Graph
    def __init__(self, graph: _Optional[_Union[Graph, _Mapping]] = ...) -> None: ...

class GetVertexRequest(_message.Message):
    __slots__ = ("key",)
    KEY_FIELD_NUMBER: _ClassVar[int]
    key: str
    def __init__(self, key: _Optional[str] = ...) -> None: ...

class GetVertexResponse(_message.Message):
    __slots__ = ("vertex",)
    VERTEX_FIELD_NUMBER: _ClassVar[int]
    vertex: Vertex
    def __init__(self, vertex: _Optional[_Union[Vertex, _Mapping]] = ...) -> None: ...

class GetVerticesRequest(_message.Message):
    __slots__ = ("keys",)
    KEYS_FIELD_NUMBER: _ClassVar[int]
    keys: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, keys: _Optional[_Iterable[str]] = ...) -> None: ...

class GetVerticesResponse(_message.Message):
    __slots__ = ("vertices", "missing")
    VERTICES_FIELD_NUMBER: _ClassVar[int]
    MISSING_FIELD_NUMBER: _ClassVar[int]
    vertices: _containers.RepeatedCompositeFieldContainer[Vertex]
    missing: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, vertices: _Optional[_Iterable[_Union[Vertex, _Mapping]]] = ..., missing: _Optional[_Iterable[str]] = ...) -> None: ...

class PutVertexRequest(_message.Message):
    __slots__ = ("vertex",)
    VERTEX_FIELD_NUMBER: _ClassVar[int]
    vertex: Vertex
    def __init__(self, vertex: _Optional[_Union[Vertex, _Mapping]] = ...) -> None: ...

class PutVertexResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class PutVerticesRequest(_message.Message):
    __slots__ = ("vertices",)
    VERTICES_FIELD_NUMBER: _ClassVar[int]
    vertices: _containers.RepeatedCompositeFieldContainer[Vertex]
    def __init__(self, vertices: _Optional[_Iterable[_Union[Vertex, _Mapping]]] = ...) -> None: ...

class PutVerticesResponse(_message.Message):
    __slots__ = ("written",)
    WRITTEN_FIELD_NUMBER: _ClassVar[int]
    written: int
    def __init__(self, written: _Optional[int] = ...) -> None: ...

class DeleteVertexRequest(_message.Message):
    __slots__ = ("key",)
    KEY_FIELD_NUMBER: _ClassVar[int]
    key: str
    def __init__(self, key: _Optional[str] = ...) -> None: ...

class DeleteVertexResponse(_message.Message):
    __slots__ = ("existed",)
    EXISTED_FIELD_NUMBER: _ClassVar[int]
    existed: bool
    def __init__(self, existed: _Optional[bool] = ...) -> None: ...

class DeleteVerticesRequest(_message.Message):
    __slots__ = ("keys",)
    KEYS_FIELD_NUMBER: _ClassVar[int]
    keys: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, keys: _Optional[_Iterable[str]] = ...) -> None: ...

class DeleteVerticesResponse(_message.Message):
    __slots__ = ("deleted",)
    DELETED_FIELD_NUMBER: _ClassVar[int]
    deleted: int
    def __init__(self, deleted: _Optional[int] = ...) -> None: ...

class ScanVerticesRequest(_message.Message):
    __slots__ = ("prefix", "limit", "cursor")
    PREFIX_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    CURSOR_FIELD_NUMBER: _ClassVar[int]
    prefix: str
    limit: int
    cursor: bytes
    def __init__(self, prefix: _Optional[str] = ..., limit: _Optional[int] = ..., cursor: _Optional[bytes] = ...) -> None: ...

class ScanVerticesResponse(_message.Message):
    __slots__ = ("vertices", "next_cursor")
    VERTICES_FIELD_NUMBER: _ClassVar[int]
    NEXT_CURSOR_FIELD_NUMBER: _ClassVar[int]
    vertices: _containers.RepeatedCompositeFieldContainer[Vertex]
    next_cursor: bytes
    def __init__(self, vertices: _Optional[_Iterable[_Union[Vertex, _Mapping]]] = ..., next_cursor: _Optional[bytes] = ...) -> None: ...

class CountVerticesByPrefixRequest(_message.Message):
    __slots__ = ("prefix",)
    PREFIX_FIELD_NUMBER: _ClassVar[int]
    prefix: str
    def __init__(self, prefix: _Optional[str] = ...) -> None: ...

class CountVerticesByPrefixResponse(_message.Message):
    __slots__ = ("count",)
    COUNT_FIELD_NUMBER: _ClassVar[int]
    count: int
    def __init__(self, count: _Optional[int] = ...) -> None: ...

class DeleteVerticesByPrefixRequest(_message.Message):
    __slots__ = ("prefix", "limit", "dry_run")
    PREFIX_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    DRY_RUN_FIELD_NUMBER: _ClassVar[int]
    prefix: str
    limit: int
    dry_run: bool
    def __init__(self, prefix: _Optional[str] = ..., limit: _Optional[int] = ..., dry_run: _Optional[bool] = ...) -> None: ...

class DeleteVerticesByPrefixResponse(_message.Message):
    __slots__ = ("deleted",)
    DELETED_FIELD_NUMBER: _ClassVar[int]
    deleted: int
    def __init__(self, deleted: _Optional[int] = ...) -> None: ...

class GetEdgeRequest(_message.Message):
    __slots__ = ("tail", "head")
    TAIL_FIELD_NUMBER: _ClassVar[int]
    HEAD_FIELD_NUMBER: _ClassVar[int]
    tail: str
    head: str
    def __init__(self, tail: _Optional[str] = ..., head: _Optional[str] = ...) -> None: ...

class GetEdgeResponse(_message.Message):
    __slots__ = ("edge",)
    EDGE_FIELD_NUMBER: _ClassVar[int]
    edge: Edge
    def __init__(self, edge: _Optional[_Union[Edge, _Mapping]] = ...) -> None: ...

class GetEdgesRequest(_message.Message):
    __slots__ = ("edges",)
    EDGES_FIELD_NUMBER: _ClassVar[int]
    edges: _containers.RepeatedCompositeFieldContainer[EdgeKey]
    def __init__(self, edges: _Optional[_Iterable[_Union[EdgeKey, _Mapping]]] = ...) -> None: ...

class GetEdgesResponse(_message.Message):
    __slots__ = ("edges", "missing")
    EDGES_FIELD_NUMBER: _ClassVar[int]
    MISSING_FIELD_NUMBER: _ClassVar[int]
    edges: _containers.RepeatedCompositeFieldContainer[Edge]
    missing: _containers.RepeatedCompositeFieldContainer[EdgeKey]
    def __init__(self, edges: _Optional[_Iterable[_Union[Edge, _Mapping]]] = ..., missing: _Optional[_Iterable[_Union[EdgeKey, _Mapping]]] = ...) -> None: ...

class DeleteEdgeRequest(_message.Message):
    __slots__ = ("tail", "head")
    TAIL_FIELD_NUMBER: _ClassVar[int]
    HEAD_FIELD_NUMBER: _ClassVar[int]
    tail: str
    head: str
    def __init__(self, tail: _Optional[str] = ..., head: _Optional[str] = ...) -> None: ...

class DeleteEdgeResponse(_message.Message):
    __slots__ = ("existed",)
    EXISTED_FIELD_NUMBER: _ClassVar[int]
    existed: bool
    def __init__(self, existed: _Optional[bool] = ...) -> None: ...

class EdgeKey(_message.Message):
    __slots__ = ("tail", "head")
    TAIL_FIELD_NUMBER: _ClassVar[int]
    HEAD_FIELD_NUMBER: _ClassVar[int]
    tail: str
    head: str
    def __init__(self, tail: _Optional[str] = ..., head: _Optional[str] = ...) -> None: ...

class ScanEdgesRequest(_message.Message):
    __slots__ = ("tail_prefix", "head_prefix", "limit", "cursor")
    TAIL_PREFIX_FIELD_NUMBER: _ClassVar[int]
    HEAD_PREFIX_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    CURSOR_FIELD_NUMBER: _ClassVar[int]
    tail_prefix: str
    head_prefix: str
    limit: int
    cursor: bytes
    def __init__(self, tail_prefix: _Optional[str] = ..., head_prefix: _Optional[str] = ..., limit: _Optional[int] = ..., cursor: _Optional[bytes] = ...) -> None: ...

class ScanEdgesResponse(_message.Message):
    __slots__ = ("edges", "next_cursor")
    EDGES_FIELD_NUMBER: _ClassVar[int]
    NEXT_CURSOR_FIELD_NUMBER: _ClassVar[int]
    edges: _containers.RepeatedCompositeFieldContainer[Edge]
    next_cursor: bytes
    def __init__(self, edges: _Optional[_Iterable[_Union[Edge, _Mapping]]] = ..., next_cursor: _Optional[bytes] = ...) -> None: ...

class DeleteEdgesRequest(_message.Message):
    __slots__ = ("edges",)
    EDGES_FIELD_NUMBER: _ClassVar[int]
    edges: _containers.RepeatedCompositeFieldContainer[EdgeKey]
    def __init__(self, edges: _Optional[_Iterable[_Union[EdgeKey, _Mapping]]] = ...) -> None: ...

class DeleteEdgesResponse(_message.Message):
    __slots__ = ("deleted",)
    DELETED_FIELD_NUMBER: _ClassVar[int]
    deleted: int
    def __init__(self, deleted: _Optional[int] = ...) -> None: ...

class AddEdgeRequest(_message.Message):
    __slots__ = ("edge",)
    EDGE_FIELD_NUMBER: _ClassVar[int]
    edge: Edge
    def __init__(self, edge: _Optional[_Union[Edge, _Mapping]] = ...) -> None: ...

class AddEdgeResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class AddEdgesRequest(_message.Message):
    __slots__ = ("edges",)
    EDGES_FIELD_NUMBER: _ClassVar[int]
    edges: _containers.RepeatedCompositeFieldContainer[Edge]
    def __init__(self, edges: _Optional[_Iterable[_Union[Edge, _Mapping]]] = ...) -> None: ...

class AddEdgesResponse(_message.Message):
    __slots__ = ("written",)
    WRITTEN_FIELD_NUMBER: _ClassVar[int]
    written: int
    def __init__(self, written: _Optional[int] = ...) -> None: ...

class PutEdgeRequest(_message.Message):
    __slots__ = ("edge",)
    EDGE_FIELD_NUMBER: _ClassVar[int]
    edge: Edge
    def __init__(self, edge: _Optional[_Union[Edge, _Mapping]] = ...) -> None: ...

class PutEdgeResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class PutEdgesRequest(_message.Message):
    __slots__ = ("edges",)
    EDGES_FIELD_NUMBER: _ClassVar[int]
    edges: _containers.RepeatedCompositeFieldContainer[Edge]
    def __init__(self, edges: _Optional[_Iterable[_Union[Edge, _Mapping]]] = ...) -> None: ...

class PutEdgesResponse(_message.Message):
    __slots__ = ("written",)
    WRITTEN_FIELD_NUMBER: _ClassVar[int]
    written: int
    def __init__(self, written: _Optional[int] = ...) -> None: ...

class GetServerStatusRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetServerStatusResponse(_message.Message):
    __slots__ = ("version", "go_version", "started_at", "uptime", "default_ttl", "max_batch_size", "max_key_bytes", "scan_default_limit", "scan_max_limit", "tls_enabled", "replication_enabled", "vertex_count", "edge_count")
    VERSION_FIELD_NUMBER: _ClassVar[int]
    GO_VERSION_FIELD_NUMBER: _ClassVar[int]
    STARTED_AT_FIELD_NUMBER: _ClassVar[int]
    UPTIME_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_TTL_FIELD_NUMBER: _ClassVar[int]
    MAX_BATCH_SIZE_FIELD_NUMBER: _ClassVar[int]
    MAX_KEY_BYTES_FIELD_NUMBER: _ClassVar[int]
    SCAN_DEFAULT_LIMIT_FIELD_NUMBER: _ClassVar[int]
    SCAN_MAX_LIMIT_FIELD_NUMBER: _ClassVar[int]
    TLS_ENABLED_FIELD_NUMBER: _ClassVar[int]
    REPLICATION_ENABLED_FIELD_NUMBER: _ClassVar[int]
    VERTEX_COUNT_FIELD_NUMBER: _ClassVar[int]
    EDGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    version: str
    go_version: str
    started_at: _timestamp_pb2.Timestamp
    uptime: _duration_pb2.Duration
    default_ttl: _duration_pb2.Duration
    max_batch_size: int
    max_key_bytes: int
    scan_default_limit: int
    scan_max_limit: int
    tls_enabled: bool
    replication_enabled: bool
    vertex_count: int
    edge_count: int
    def __init__(self, version: _Optional[str] = ..., go_version: _Optional[str] = ..., started_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., uptime: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., default_ttl: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., max_batch_size: _Optional[int] = ..., max_key_bytes: _Optional[int] = ..., scan_default_limit: _Optional[int] = ..., scan_max_limit: _Optional[int] = ..., tls_enabled: _Optional[bool] = ..., replication_enabled: _Optional[bool] = ..., vertex_count: _Optional[int] = ..., edge_count: _Optional[int] = ...) -> None: ...

class ReplicationPeer(_message.Message):
    __slots__ = ("address", "state", "last_event_at", "applied_seq", "error")
    class State(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        STATE_UNSPECIFIED: _ClassVar[ReplicationPeer.State]
        STATE_CONNECTING: _ClassVar[ReplicationPeer.State]
        STATE_STREAMING: _ClassVar[ReplicationPeer.State]
        STATE_BACKOFF: _ClassVar[ReplicationPeer.State]
        STATE_CLOSED: _ClassVar[ReplicationPeer.State]
    STATE_UNSPECIFIED: ReplicationPeer.State
    STATE_CONNECTING: ReplicationPeer.State
    STATE_STREAMING: ReplicationPeer.State
    STATE_BACKOFF: ReplicationPeer.State
    STATE_CLOSED: ReplicationPeer.State
    ADDRESS_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    LAST_EVENT_AT_FIELD_NUMBER: _ClassVar[int]
    APPLIED_SEQ_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    address: str
    state: ReplicationPeer.State
    last_event_at: _timestamp_pb2.Timestamp
    applied_seq: int
    error: str
    def __init__(self, address: _Optional[str] = ..., state: _Optional[_Union[ReplicationPeer.State, str]] = ..., last_event_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., applied_seq: _Optional[int] = ..., error: _Optional[str] = ...) -> None: ...

class GetReplicationStatusRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetReplicationStatusResponse(_message.Message):
    __slots__ = ("node_id", "local_now", "enabled", "peers")
    NODE_ID_FIELD_NUMBER: _ClassVar[int]
    LOCAL_NOW_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    PEERS_FIELD_NUMBER: _ClassVar[int]
    node_id: str
    local_now: _timestamp_pb2.Timestamp
    enabled: bool
    peers: _containers.RepeatedCompositeFieldContainer[ReplicationPeer]
    def __init__(self, node_id: _Optional[str] = ..., local_now: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., enabled: _Optional[bool] = ..., peers: _Optional[_Iterable[_Union[ReplicationPeer, _Mapping]]] = ...) -> None: ...
