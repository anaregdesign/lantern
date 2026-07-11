// This is a generated file - do not edit.
//
// Generated from graph/v1/graph.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:grpc/service_api.dart' as $grpc;
import 'package:protobuf/protobuf.dart' as $pb;

import 'graph.pb.dart' as $0;

export 'graph.pb.dart';

@$pb.GrpcServiceName('graph.v1.LanternService')
class LanternServiceClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  LanternServiceClient(super.channel, {super.options, super.interceptors});

  $grpc.ResponseFuture<$0.IlluminateResponse> illuminate(
    $0.IlluminateRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$illuminate, request, options: options);
  }

  $grpc.ResponseFuture<$0.GetVertexResponse> getVertex(
    $0.GetVertexRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getVertex, request, options: options);
  }

  /// GetVertices reads several vertices in one round trip.
  $grpc.ResponseFuture<$0.GetVerticesResponse> getVertices(
    $0.GetVerticesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getVertices, request, options: options);
  }

  /// PutVertex writes a single vertex. Thin facade over PutVertices.
  $grpc.ResponseFuture<$0.PutVertexResponse> putVertex(
    $0.PutVertexRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$putVertex, request, options: options);
  }

  $grpc.ResponseFuture<$0.PutVerticesResponse> putVertices(
    $0.PutVerticesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$putVertices, request, options: options);
  }

  $grpc.ResponseFuture<$0.DeleteVertexResponse> deleteVertex(
    $0.DeleteVertexRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$deleteVertex, request, options: options);
  }

  /// DeleteVertices removes several vertices in one round trip.
  $grpc.ResponseFuture<$0.DeleteVerticesResponse> deleteVertices(
    $0.DeleteVerticesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$deleteVertices, request, options: options);
  }

  /// ScanVertices streams vertices whose key starts with the given prefix in
  /// ascending order, page by page. Plural-only — prefix scan is inherently
  /// plural.
  $grpc.ResponseFuture<$0.ScanVerticesResponse> scanVertices(
    $0.ScanVerticesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$scanVertices, request, options: options);
  }

  /// ScanVertexKeys streams just the KEYS (no values) of vertices whose key
  /// starts with the given prefix, page by page — the wire-efficient backing
  /// RPC for the `keys` CLI verb. A non-empty prefix is REQUIRED. Plural-only.
  $grpc.ResponseFuture<$0.ScanVertexKeysResponse> scanVertexKeys(
    $0.ScanVertexKeysRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$scanVertexKeys, request, options: options);
  }

  /// SearchVertices returns vertices ranked by full-text relevance over their
  /// content (key + value), optionally scoped to a key prefix. Requires the
  /// server-side search index (LANTERN_SEARCH_ENABLED, on by default);
  /// returns FAILED_PRECONDITION when disabled. Plural-only — ranked search
  /// is inherently plural.
  $grpc.ResponseFuture<$0.SearchVerticesResponse> searchVertices(
    $0.SearchVerticesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$searchVertices, request, options: options);
  }

  /// CountVerticesByPrefix returns the number of live vertices whose key
  /// starts with the given prefix.
  $grpc.ResponseFuture<$0.CountVerticesByPrefixResponse> countVerticesByPrefix(
    $0.CountVerticesByPrefixRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$countVerticesByPrefix, request, options: options);
  }

  /// DeleteVerticesByPrefix deletes up to `limit` vertices whose key starts
  /// with the given prefix. Pass `dry_run = true` to preview the count
  /// without mutating state.
  $grpc.ResponseFuture<$0.DeleteVerticesByPrefixResponse>
      deleteVerticesByPrefix(
    $0.DeleteVerticesByPrefixRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$deleteVerticesByPrefix, request,
        options: options);
  }

  /// TopVerticesByDegree ranks the most-connected live vertices under a key
  /// prefix by their (weighted) out/in/both degree. Read-only aggregate; a
  /// non-empty prefix is REQUIRED (empty → INVALID_ARGUMENT). Results are
  /// point-in-time best-effort and honour the live-visibility rule (#750).
  ///
  /// Cost model (#920): OUT-degree is scoped to the candidates' own out-edges,
  /// but IN and BOTH scan every edge bucket in the graph — O(E_total),
  /// independent of prefix narrowness, because no reverse (head->tails) index
  /// exists. That scan runs without holding the write-blocking aggregate lock
  /// for its full duration, so an IN/BOTH result on a large, actively-written
  /// graph is a best-effort snapshot rather than a single point-in-time view.
  $grpc.ResponseFuture<$0.TopVerticesByDegreeResponse> topVerticesByDegree(
    $0.TopVerticesByDegreeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$topVerticesByDegree, request, options: options);
  }

  $grpc.ResponseFuture<$0.GetEdgeResponse> getEdge(
    $0.GetEdgeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getEdge, request, options: options);
  }

  /// GetEdges reads several edges in one round trip.
  $grpc.ResponseFuture<$0.GetEdgesResponse> getEdges(
    $0.GetEdgesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getEdges, request, options: options);
  }

  /// AddEdge is non-idempotent (accumulates weight). Thin facade over AddEdges.
  $grpc.ResponseFuture<$0.AddEdgeResponse> addEdge(
    $0.AddEdgeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$addEdge, request, options: options);
  }

  /// AddEdges is non-idempotent (accumulates weight). POST per REST conventions.
  $grpc.ResponseFuture<$0.AddEdgesResponse> addEdges(
    $0.AddEdgesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$addEdges, request, options: options);
  }

  /// PutEdge is idempotent (replaces weight). Thin facade over PutEdges.
  $grpc.ResponseFuture<$0.PutEdgeResponse> putEdge(
    $0.PutEdgeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$putEdge, request, options: options);
  }

  /// PutEdges is idempotent (replaces weight). PUT per REST conventions.
  $grpc.ResponseFuture<$0.PutEdgesResponse> putEdges(
    $0.PutEdgesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$putEdges, request, options: options);
  }

  $grpc.ResponseFuture<$0.DeleteEdgeResponse> deleteEdge(
    $0.DeleteEdgeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$deleteEdge, request, options: options);
  }

  /// DeleteEdges removes several edges in one round trip.
  $grpc.ResponseFuture<$0.DeleteEdgesResponse> deleteEdges(
    $0.DeleteEdgesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$deleteEdges, request, options: options);
  }

  /// DeleteEdgesByPrefix deletes up to `limit` live edges whose tail key
  /// starts with `tail_prefix` AND whose head key starts with `head_prefix`.
  /// At least one prefix must be non-empty. Pass `dry_run = true` to preview
  /// the count without mutating state. Plural-only — prefix delete is
  /// inherently plural.
  $grpc.ResponseFuture<$0.DeleteEdgesByPrefixResponse> deleteEdgesByPrefix(
    $0.DeleteEdgesByPrefixRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$deleteEdgesByPrefix, request, options: options);
  }

  /// ScanEdges streams edges whose tail key starts with `tail_prefix` AND
  /// whose head key starts with `head_prefix`, in ascending (tail, head)
  /// order, page by page. Plural-only — prefix scan is inherently plural.
  $grpc.ResponseFuture<$0.ScanEdgesResponse> scanEdges(
    $0.ScanEdgesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$scanEdges, request, options: options);
  }

  /// GetServerStatus returns a flat snapshot of the server's identity,
  /// build info, configuration ceilings, and current live vertex/edge
  /// counts. Read-only and cheap — intended for the admin UI's "Ops"
  /// tab and lightweight smoke-test tooling. Auth is the caller's
  /// responsibility; no PII is returned.
  $grpc.ResponseFuture<$0.GetServerStatusResponse> getServerStatus(
    $0.GetServerStatusRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getServerStatus, request, options: options);
  }

  /// GetReplicationStatus returns a flat snapshot of the local node's
  /// outbound peer-replication state. Read-only — no peer add/remove
  /// surface is exposed (see #315 out-of-scope). Cheap to call from a
  /// dashboard at any cadence the operator finds useful. On
  /// single-instance deployments enabled=false and peers is empty.
  $grpc.ResponseFuture<$0.GetReplicationStatusResponse> getReplicationStatus(
    $0.GetReplicationStatusRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getReplicationStatus, request, options: options);
  }

  /// BackupSnapshot streams a whole-graph, point-in-time backup: every
  /// live vertex and folded edge as a BackupRecord, materialised under a
  /// single GraphCache lock (SnapshotGraph). Unlike the replication
  /// Snapshot RPC it has NO replication gate — it works on a single node.
  /// vertex_prefix optionally scopes the backup to an induced subgraph.
  /// The restore side replays records through PutVertices / PutEdges, so
  /// there is no dedicated restore RPC.
  $grpc.ResponseStream<$0.BackupSnapshotResponse> backupSnapshot(
    $0.BackupSnapshotRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createStreamingCall(
        _$backupSnapshot, $async.Stream.fromIterable([request]),
        options: options);
  }

  // method descriptors

  static final _$illuminate =
      $grpc.ClientMethod<$0.IlluminateRequest, $0.IlluminateResponse>(
          '/graph.v1.LanternService/Illuminate',
          ($0.IlluminateRequest value) => value.writeToBuffer(),
          $0.IlluminateResponse.fromBuffer);
  static final _$getVertex =
      $grpc.ClientMethod<$0.GetVertexRequest, $0.GetVertexResponse>(
          '/graph.v1.LanternService/GetVertex',
          ($0.GetVertexRequest value) => value.writeToBuffer(),
          $0.GetVertexResponse.fromBuffer);
  static final _$getVertices =
      $grpc.ClientMethod<$0.GetVerticesRequest, $0.GetVerticesResponse>(
          '/graph.v1.LanternService/GetVertices',
          ($0.GetVerticesRequest value) => value.writeToBuffer(),
          $0.GetVerticesResponse.fromBuffer);
  static final _$putVertex =
      $grpc.ClientMethod<$0.PutVertexRequest, $0.PutVertexResponse>(
          '/graph.v1.LanternService/PutVertex',
          ($0.PutVertexRequest value) => value.writeToBuffer(),
          $0.PutVertexResponse.fromBuffer);
  static final _$putVertices =
      $grpc.ClientMethod<$0.PutVerticesRequest, $0.PutVerticesResponse>(
          '/graph.v1.LanternService/PutVertices',
          ($0.PutVerticesRequest value) => value.writeToBuffer(),
          $0.PutVerticesResponse.fromBuffer);
  static final _$deleteVertex =
      $grpc.ClientMethod<$0.DeleteVertexRequest, $0.DeleteVertexResponse>(
          '/graph.v1.LanternService/DeleteVertex',
          ($0.DeleteVertexRequest value) => value.writeToBuffer(),
          $0.DeleteVertexResponse.fromBuffer);
  static final _$deleteVertices =
      $grpc.ClientMethod<$0.DeleteVerticesRequest, $0.DeleteVerticesResponse>(
          '/graph.v1.LanternService/DeleteVertices',
          ($0.DeleteVerticesRequest value) => value.writeToBuffer(),
          $0.DeleteVerticesResponse.fromBuffer);
  static final _$scanVertices =
      $grpc.ClientMethod<$0.ScanVerticesRequest, $0.ScanVerticesResponse>(
          '/graph.v1.LanternService/ScanVertices',
          ($0.ScanVerticesRequest value) => value.writeToBuffer(),
          $0.ScanVerticesResponse.fromBuffer);
  static final _$scanVertexKeys =
      $grpc.ClientMethod<$0.ScanVertexKeysRequest, $0.ScanVertexKeysResponse>(
          '/graph.v1.LanternService/ScanVertexKeys',
          ($0.ScanVertexKeysRequest value) => value.writeToBuffer(),
          $0.ScanVertexKeysResponse.fromBuffer);
  static final _$searchVertices =
      $grpc.ClientMethod<$0.SearchVerticesRequest, $0.SearchVerticesResponse>(
          '/graph.v1.LanternService/SearchVertices',
          ($0.SearchVerticesRequest value) => value.writeToBuffer(),
          $0.SearchVerticesResponse.fromBuffer);
  static final _$countVerticesByPrefix = $grpc.ClientMethod<
          $0.CountVerticesByPrefixRequest, $0.CountVerticesByPrefixResponse>(
      '/graph.v1.LanternService/CountVerticesByPrefix',
      ($0.CountVerticesByPrefixRequest value) => value.writeToBuffer(),
      $0.CountVerticesByPrefixResponse.fromBuffer);
  static final _$deleteVerticesByPrefix = $grpc.ClientMethod<
          $0.DeleteVerticesByPrefixRequest, $0.DeleteVerticesByPrefixResponse>(
      '/graph.v1.LanternService/DeleteVerticesByPrefix',
      ($0.DeleteVerticesByPrefixRequest value) => value.writeToBuffer(),
      $0.DeleteVerticesByPrefixResponse.fromBuffer);
  static final _$topVerticesByDegree = $grpc.ClientMethod<
          $0.TopVerticesByDegreeRequest, $0.TopVerticesByDegreeResponse>(
      '/graph.v1.LanternService/TopVerticesByDegree',
      ($0.TopVerticesByDegreeRequest value) => value.writeToBuffer(),
      $0.TopVerticesByDegreeResponse.fromBuffer);
  static final _$getEdge =
      $grpc.ClientMethod<$0.GetEdgeRequest, $0.GetEdgeResponse>(
          '/graph.v1.LanternService/GetEdge',
          ($0.GetEdgeRequest value) => value.writeToBuffer(),
          $0.GetEdgeResponse.fromBuffer);
  static final _$getEdges =
      $grpc.ClientMethod<$0.GetEdgesRequest, $0.GetEdgesResponse>(
          '/graph.v1.LanternService/GetEdges',
          ($0.GetEdgesRequest value) => value.writeToBuffer(),
          $0.GetEdgesResponse.fromBuffer);
  static final _$addEdge =
      $grpc.ClientMethod<$0.AddEdgeRequest, $0.AddEdgeResponse>(
          '/graph.v1.LanternService/AddEdge',
          ($0.AddEdgeRequest value) => value.writeToBuffer(),
          $0.AddEdgeResponse.fromBuffer);
  static final _$addEdges =
      $grpc.ClientMethod<$0.AddEdgesRequest, $0.AddEdgesResponse>(
          '/graph.v1.LanternService/AddEdges',
          ($0.AddEdgesRequest value) => value.writeToBuffer(),
          $0.AddEdgesResponse.fromBuffer);
  static final _$putEdge =
      $grpc.ClientMethod<$0.PutEdgeRequest, $0.PutEdgeResponse>(
          '/graph.v1.LanternService/PutEdge',
          ($0.PutEdgeRequest value) => value.writeToBuffer(),
          $0.PutEdgeResponse.fromBuffer);
  static final _$putEdges =
      $grpc.ClientMethod<$0.PutEdgesRequest, $0.PutEdgesResponse>(
          '/graph.v1.LanternService/PutEdges',
          ($0.PutEdgesRequest value) => value.writeToBuffer(),
          $0.PutEdgesResponse.fromBuffer);
  static final _$deleteEdge =
      $grpc.ClientMethod<$0.DeleteEdgeRequest, $0.DeleteEdgeResponse>(
          '/graph.v1.LanternService/DeleteEdge',
          ($0.DeleteEdgeRequest value) => value.writeToBuffer(),
          $0.DeleteEdgeResponse.fromBuffer);
  static final _$deleteEdges =
      $grpc.ClientMethod<$0.DeleteEdgesRequest, $0.DeleteEdgesResponse>(
          '/graph.v1.LanternService/DeleteEdges',
          ($0.DeleteEdgesRequest value) => value.writeToBuffer(),
          $0.DeleteEdgesResponse.fromBuffer);
  static final _$deleteEdgesByPrefix = $grpc.ClientMethod<
          $0.DeleteEdgesByPrefixRequest, $0.DeleteEdgesByPrefixResponse>(
      '/graph.v1.LanternService/DeleteEdgesByPrefix',
      ($0.DeleteEdgesByPrefixRequest value) => value.writeToBuffer(),
      $0.DeleteEdgesByPrefixResponse.fromBuffer);
  static final _$scanEdges =
      $grpc.ClientMethod<$0.ScanEdgesRequest, $0.ScanEdgesResponse>(
          '/graph.v1.LanternService/ScanEdges',
          ($0.ScanEdgesRequest value) => value.writeToBuffer(),
          $0.ScanEdgesResponse.fromBuffer);
  static final _$getServerStatus =
      $grpc.ClientMethod<$0.GetServerStatusRequest, $0.GetServerStatusResponse>(
          '/graph.v1.LanternService/GetServerStatus',
          ($0.GetServerStatusRequest value) => value.writeToBuffer(),
          $0.GetServerStatusResponse.fromBuffer);
  static final _$getReplicationStatus = $grpc.ClientMethod<
          $0.GetReplicationStatusRequest, $0.GetReplicationStatusResponse>(
      '/graph.v1.LanternService/GetReplicationStatus',
      ($0.GetReplicationStatusRequest value) => value.writeToBuffer(),
      $0.GetReplicationStatusResponse.fromBuffer);
  static final _$backupSnapshot =
      $grpc.ClientMethod<$0.BackupSnapshotRequest, $0.BackupSnapshotResponse>(
          '/graph.v1.LanternService/BackupSnapshot',
          ($0.BackupSnapshotRequest value) => value.writeToBuffer(),
          $0.BackupSnapshotResponse.fromBuffer);
}

@$pb.GrpcServiceName('graph.v1.LanternService')
abstract class LanternServiceBase extends $grpc.Service {
  $core.String get $name => 'graph.v1.LanternService';

  LanternServiceBase() {
    $addMethod($grpc.ServiceMethod<$0.IlluminateRequest, $0.IlluminateResponse>(
        'Illuminate',
        illuminate_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.IlluminateRequest.fromBuffer(value),
        ($0.IlluminateResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetVertexRequest, $0.GetVertexResponse>(
        'GetVertex',
        getVertex_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.GetVertexRequest.fromBuffer(value),
        ($0.GetVertexResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.GetVerticesRequest, $0.GetVerticesResponse>(
            'GetVertices',
            getVertices_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.GetVerticesRequest.fromBuffer(value),
            ($0.GetVerticesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.PutVertexRequest, $0.PutVertexResponse>(
        'PutVertex',
        putVertex_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.PutVertexRequest.fromBuffer(value),
        ($0.PutVertexResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.PutVerticesRequest, $0.PutVerticesResponse>(
            'PutVertices',
            putVertices_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.PutVerticesRequest.fromBuffer(value),
            ($0.PutVerticesResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.DeleteVertexRequest, $0.DeleteVertexResponse>(
            'DeleteVertex',
            deleteVertex_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.DeleteVertexRequest.fromBuffer(value),
            ($0.DeleteVertexResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.DeleteVerticesRequest,
            $0.DeleteVerticesResponse>(
        'DeleteVertices',
        deleteVertices_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.DeleteVerticesRequest.fromBuffer(value),
        ($0.DeleteVerticesResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.ScanVerticesRequest, $0.ScanVerticesResponse>(
            'ScanVertices',
            scanVertices_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.ScanVerticesRequest.fromBuffer(value),
            ($0.ScanVerticesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ScanVertexKeysRequest,
            $0.ScanVertexKeysResponse>(
        'ScanVertexKeys',
        scanVertexKeys_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ScanVertexKeysRequest.fromBuffer(value),
        ($0.ScanVertexKeysResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.SearchVerticesRequest,
            $0.SearchVerticesResponse>(
        'SearchVertices',
        searchVertices_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.SearchVerticesRequest.fromBuffer(value),
        ($0.SearchVerticesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.CountVerticesByPrefixRequest,
            $0.CountVerticesByPrefixResponse>(
        'CountVerticesByPrefix',
        countVerticesByPrefix_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.CountVerticesByPrefixRequest.fromBuffer(value),
        ($0.CountVerticesByPrefixResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.DeleteVerticesByPrefixRequest,
            $0.DeleteVerticesByPrefixResponse>(
        'DeleteVerticesByPrefix',
        deleteVerticesByPrefix_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.DeleteVerticesByPrefixRequest.fromBuffer(value),
        ($0.DeleteVerticesByPrefixResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.TopVerticesByDegreeRequest,
            $0.TopVerticesByDegreeResponse>(
        'TopVerticesByDegree',
        topVerticesByDegree_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.TopVerticesByDegreeRequest.fromBuffer(value),
        ($0.TopVerticesByDegreeResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetEdgeRequest, $0.GetEdgeResponse>(
        'GetEdge',
        getEdge_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.GetEdgeRequest.fromBuffer(value),
        ($0.GetEdgeResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetEdgesRequest, $0.GetEdgesResponse>(
        'GetEdges',
        getEdges_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.GetEdgesRequest.fromBuffer(value),
        ($0.GetEdgesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.AddEdgeRequest, $0.AddEdgeResponse>(
        'AddEdge',
        addEdge_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.AddEdgeRequest.fromBuffer(value),
        ($0.AddEdgeResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.AddEdgesRequest, $0.AddEdgesResponse>(
        'AddEdges',
        addEdges_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.AddEdgesRequest.fromBuffer(value),
        ($0.AddEdgesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.PutEdgeRequest, $0.PutEdgeResponse>(
        'PutEdge',
        putEdge_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.PutEdgeRequest.fromBuffer(value),
        ($0.PutEdgeResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.PutEdgesRequest, $0.PutEdgesResponse>(
        'PutEdges',
        putEdges_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.PutEdgesRequest.fromBuffer(value),
        ($0.PutEdgesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.DeleteEdgeRequest, $0.DeleteEdgeResponse>(
        'DeleteEdge',
        deleteEdge_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.DeleteEdgeRequest.fromBuffer(value),
        ($0.DeleteEdgeResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.DeleteEdgesRequest, $0.DeleteEdgesResponse>(
            'DeleteEdges',
            deleteEdges_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.DeleteEdgesRequest.fromBuffer(value),
            ($0.DeleteEdgesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.DeleteEdgesByPrefixRequest,
            $0.DeleteEdgesByPrefixResponse>(
        'DeleteEdgesByPrefix',
        deleteEdgesByPrefix_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.DeleteEdgesByPrefixRequest.fromBuffer(value),
        ($0.DeleteEdgesByPrefixResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ScanEdgesRequest, $0.ScanEdgesResponse>(
        'ScanEdges',
        scanEdges_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.ScanEdgesRequest.fromBuffer(value),
        ($0.ScanEdgesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetServerStatusRequest,
            $0.GetServerStatusResponse>(
        'GetServerStatus',
        getServerStatus_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.GetServerStatusRequest.fromBuffer(value),
        ($0.GetServerStatusResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetReplicationStatusRequest,
            $0.GetReplicationStatusResponse>(
        'GetReplicationStatus',
        getReplicationStatus_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.GetReplicationStatusRequest.fromBuffer(value),
        ($0.GetReplicationStatusResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.BackupSnapshotRequest,
            $0.BackupSnapshotResponse>(
        'BackupSnapshot',
        backupSnapshot_Pre,
        false,
        true,
        ($core.List<$core.int> value) =>
            $0.BackupSnapshotRequest.fromBuffer(value),
        ($0.BackupSnapshotResponse value) => value.writeToBuffer()));
  }

  $async.Future<$0.IlluminateResponse> illuminate_Pre($grpc.ServiceCall $call,
      $async.Future<$0.IlluminateRequest> $request) async {
    return illuminate($call, await $request);
  }

  $async.Future<$0.IlluminateResponse> illuminate(
      $grpc.ServiceCall call, $0.IlluminateRequest request);

  $async.Future<$0.GetVertexResponse> getVertex_Pre($grpc.ServiceCall $call,
      $async.Future<$0.GetVertexRequest> $request) async {
    return getVertex($call, await $request);
  }

  $async.Future<$0.GetVertexResponse> getVertex(
      $grpc.ServiceCall call, $0.GetVertexRequest request);

  $async.Future<$0.GetVerticesResponse> getVertices_Pre($grpc.ServiceCall $call,
      $async.Future<$0.GetVerticesRequest> $request) async {
    return getVertices($call, await $request);
  }

  $async.Future<$0.GetVerticesResponse> getVertices(
      $grpc.ServiceCall call, $0.GetVerticesRequest request);

  $async.Future<$0.PutVertexResponse> putVertex_Pre($grpc.ServiceCall $call,
      $async.Future<$0.PutVertexRequest> $request) async {
    return putVertex($call, await $request);
  }

  $async.Future<$0.PutVertexResponse> putVertex(
      $grpc.ServiceCall call, $0.PutVertexRequest request);

  $async.Future<$0.PutVerticesResponse> putVertices_Pre($grpc.ServiceCall $call,
      $async.Future<$0.PutVerticesRequest> $request) async {
    return putVertices($call, await $request);
  }

  $async.Future<$0.PutVerticesResponse> putVertices(
      $grpc.ServiceCall call, $0.PutVerticesRequest request);

  $async.Future<$0.DeleteVertexResponse> deleteVertex_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.DeleteVertexRequest> $request) async {
    return deleteVertex($call, await $request);
  }

  $async.Future<$0.DeleteVertexResponse> deleteVertex(
      $grpc.ServiceCall call, $0.DeleteVertexRequest request);

  $async.Future<$0.DeleteVerticesResponse> deleteVertices_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.DeleteVerticesRequest> $request) async {
    return deleteVertices($call, await $request);
  }

  $async.Future<$0.DeleteVerticesResponse> deleteVertices(
      $grpc.ServiceCall call, $0.DeleteVerticesRequest request);

  $async.Future<$0.ScanVerticesResponse> scanVertices_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ScanVerticesRequest> $request) async {
    return scanVertices($call, await $request);
  }

  $async.Future<$0.ScanVerticesResponse> scanVertices(
      $grpc.ServiceCall call, $0.ScanVerticesRequest request);

  $async.Future<$0.ScanVertexKeysResponse> scanVertexKeys_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ScanVertexKeysRequest> $request) async {
    return scanVertexKeys($call, await $request);
  }

  $async.Future<$0.ScanVertexKeysResponse> scanVertexKeys(
      $grpc.ServiceCall call, $0.ScanVertexKeysRequest request);

  $async.Future<$0.SearchVerticesResponse> searchVertices_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.SearchVerticesRequest> $request) async {
    return searchVertices($call, await $request);
  }

  $async.Future<$0.SearchVerticesResponse> searchVertices(
      $grpc.ServiceCall call, $0.SearchVerticesRequest request);

  $async.Future<$0.CountVerticesByPrefixResponse> countVerticesByPrefix_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.CountVerticesByPrefixRequest> $request) async {
    return countVerticesByPrefix($call, await $request);
  }

  $async.Future<$0.CountVerticesByPrefixResponse> countVerticesByPrefix(
      $grpc.ServiceCall call, $0.CountVerticesByPrefixRequest request);

  $async.Future<$0.DeleteVerticesByPrefixResponse> deleteVerticesByPrefix_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.DeleteVerticesByPrefixRequest> $request) async {
    return deleteVerticesByPrefix($call, await $request);
  }

  $async.Future<$0.DeleteVerticesByPrefixResponse> deleteVerticesByPrefix(
      $grpc.ServiceCall call, $0.DeleteVerticesByPrefixRequest request);

  $async.Future<$0.TopVerticesByDegreeResponse> topVerticesByDegree_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.TopVerticesByDegreeRequest> $request) async {
    return topVerticesByDegree($call, await $request);
  }

  $async.Future<$0.TopVerticesByDegreeResponse> topVerticesByDegree(
      $grpc.ServiceCall call, $0.TopVerticesByDegreeRequest request);

  $async.Future<$0.GetEdgeResponse> getEdge_Pre($grpc.ServiceCall $call,
      $async.Future<$0.GetEdgeRequest> $request) async {
    return getEdge($call, await $request);
  }

  $async.Future<$0.GetEdgeResponse> getEdge(
      $grpc.ServiceCall call, $0.GetEdgeRequest request);

  $async.Future<$0.GetEdgesResponse> getEdges_Pre($grpc.ServiceCall $call,
      $async.Future<$0.GetEdgesRequest> $request) async {
    return getEdges($call, await $request);
  }

  $async.Future<$0.GetEdgesResponse> getEdges(
      $grpc.ServiceCall call, $0.GetEdgesRequest request);

  $async.Future<$0.AddEdgeResponse> addEdge_Pre($grpc.ServiceCall $call,
      $async.Future<$0.AddEdgeRequest> $request) async {
    return addEdge($call, await $request);
  }

  $async.Future<$0.AddEdgeResponse> addEdge(
      $grpc.ServiceCall call, $0.AddEdgeRequest request);

  $async.Future<$0.AddEdgesResponse> addEdges_Pre($grpc.ServiceCall $call,
      $async.Future<$0.AddEdgesRequest> $request) async {
    return addEdges($call, await $request);
  }

  $async.Future<$0.AddEdgesResponse> addEdges(
      $grpc.ServiceCall call, $0.AddEdgesRequest request);

  $async.Future<$0.PutEdgeResponse> putEdge_Pre($grpc.ServiceCall $call,
      $async.Future<$0.PutEdgeRequest> $request) async {
    return putEdge($call, await $request);
  }

  $async.Future<$0.PutEdgeResponse> putEdge(
      $grpc.ServiceCall call, $0.PutEdgeRequest request);

  $async.Future<$0.PutEdgesResponse> putEdges_Pre($grpc.ServiceCall $call,
      $async.Future<$0.PutEdgesRequest> $request) async {
    return putEdges($call, await $request);
  }

  $async.Future<$0.PutEdgesResponse> putEdges(
      $grpc.ServiceCall call, $0.PutEdgesRequest request);

  $async.Future<$0.DeleteEdgeResponse> deleteEdge_Pre($grpc.ServiceCall $call,
      $async.Future<$0.DeleteEdgeRequest> $request) async {
    return deleteEdge($call, await $request);
  }

  $async.Future<$0.DeleteEdgeResponse> deleteEdge(
      $grpc.ServiceCall call, $0.DeleteEdgeRequest request);

  $async.Future<$0.DeleteEdgesResponse> deleteEdges_Pre($grpc.ServiceCall $call,
      $async.Future<$0.DeleteEdgesRequest> $request) async {
    return deleteEdges($call, await $request);
  }

  $async.Future<$0.DeleteEdgesResponse> deleteEdges(
      $grpc.ServiceCall call, $0.DeleteEdgesRequest request);

  $async.Future<$0.DeleteEdgesByPrefixResponse> deleteEdgesByPrefix_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.DeleteEdgesByPrefixRequest> $request) async {
    return deleteEdgesByPrefix($call, await $request);
  }

  $async.Future<$0.DeleteEdgesByPrefixResponse> deleteEdgesByPrefix(
      $grpc.ServiceCall call, $0.DeleteEdgesByPrefixRequest request);

  $async.Future<$0.ScanEdgesResponse> scanEdges_Pre($grpc.ServiceCall $call,
      $async.Future<$0.ScanEdgesRequest> $request) async {
    return scanEdges($call, await $request);
  }

  $async.Future<$0.ScanEdgesResponse> scanEdges(
      $grpc.ServiceCall call, $0.ScanEdgesRequest request);

  $async.Future<$0.GetServerStatusResponse> getServerStatus_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.GetServerStatusRequest> $request) async {
    return getServerStatus($call, await $request);
  }

  $async.Future<$0.GetServerStatusResponse> getServerStatus(
      $grpc.ServiceCall call, $0.GetServerStatusRequest request);

  $async.Future<$0.GetReplicationStatusResponse> getReplicationStatus_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.GetReplicationStatusRequest> $request) async {
    return getReplicationStatus($call, await $request);
  }

  $async.Future<$0.GetReplicationStatusResponse> getReplicationStatus(
      $grpc.ServiceCall call, $0.GetReplicationStatusRequest request);

  $async.Stream<$0.BackupSnapshotResponse> backupSnapshot_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.BackupSnapshotRequest> $request) async* {
    yield* backupSnapshot($call, await $request);
  }

  $async.Stream<$0.BackupSnapshotResponse> backupSnapshot(
      $grpc.ServiceCall call, $0.BackupSnapshotRequest request);
}
