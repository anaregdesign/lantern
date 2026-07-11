// This is a generated file - do not edit.
//
// Generated from graph/v1/graph.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

import 'graph.pb.dart' as $2;
import 'graph.pbjson.dart';

export 'graph.pb.dart';

abstract class LanternServiceBase extends $pb.GeneratedService {
  $async.Future<$2.IlluminateResponse> illuminate(
      $pb.ServerContext ctx, $2.IlluminateRequest request);
  $async.Future<$2.GetVertexResponse> getVertex(
      $pb.ServerContext ctx, $2.GetVertexRequest request);
  $async.Future<$2.GetVerticesResponse> getVertices(
      $pb.ServerContext ctx, $2.GetVerticesRequest request);
  $async.Future<$2.PutVertexResponse> putVertex(
      $pb.ServerContext ctx, $2.PutVertexRequest request);
  $async.Future<$2.PutVerticesResponse> putVertices(
      $pb.ServerContext ctx, $2.PutVerticesRequest request);
  $async.Future<$2.DeleteVertexResponse> deleteVertex(
      $pb.ServerContext ctx, $2.DeleteVertexRequest request);
  $async.Future<$2.DeleteVerticesResponse> deleteVertices(
      $pb.ServerContext ctx, $2.DeleteVerticesRequest request);
  $async.Future<$2.ScanVerticesResponse> scanVertices(
      $pb.ServerContext ctx, $2.ScanVerticesRequest request);
  $async.Future<$2.ScanVertexKeysResponse> scanVertexKeys(
      $pb.ServerContext ctx, $2.ScanVertexKeysRequest request);
  $async.Future<$2.SearchVerticesResponse> searchVertices(
      $pb.ServerContext ctx, $2.SearchVerticesRequest request);
  $async.Future<$2.CountVerticesByPrefixResponse> countVerticesByPrefix(
      $pb.ServerContext ctx, $2.CountVerticesByPrefixRequest request);
  $async.Future<$2.DeleteVerticesByPrefixResponse> deleteVerticesByPrefix(
      $pb.ServerContext ctx, $2.DeleteVerticesByPrefixRequest request);
  $async.Future<$2.TopVerticesByDegreeResponse> topVerticesByDegree(
      $pb.ServerContext ctx, $2.TopVerticesByDegreeRequest request);
  $async.Future<$2.GetEdgeResponse> getEdge(
      $pb.ServerContext ctx, $2.GetEdgeRequest request);
  $async.Future<$2.GetEdgesResponse> getEdges(
      $pb.ServerContext ctx, $2.GetEdgesRequest request);
  $async.Future<$2.AddEdgeResponse> addEdge(
      $pb.ServerContext ctx, $2.AddEdgeRequest request);
  $async.Future<$2.AddEdgesResponse> addEdges(
      $pb.ServerContext ctx, $2.AddEdgesRequest request);
  $async.Future<$2.PutEdgeResponse> putEdge(
      $pb.ServerContext ctx, $2.PutEdgeRequest request);
  $async.Future<$2.PutEdgesResponse> putEdges(
      $pb.ServerContext ctx, $2.PutEdgesRequest request);
  $async.Future<$2.DeleteEdgeResponse> deleteEdge(
      $pb.ServerContext ctx, $2.DeleteEdgeRequest request);
  $async.Future<$2.DeleteEdgesResponse> deleteEdges(
      $pb.ServerContext ctx, $2.DeleteEdgesRequest request);
  $async.Future<$2.DeleteEdgesByPrefixResponse> deleteEdgesByPrefix(
      $pb.ServerContext ctx, $2.DeleteEdgesByPrefixRequest request);
  $async.Future<$2.ScanEdgesResponse> scanEdges(
      $pb.ServerContext ctx, $2.ScanEdgesRequest request);
  $async.Future<$2.GetServerStatusResponse> getServerStatus(
      $pb.ServerContext ctx, $2.GetServerStatusRequest request);
  $async.Future<$2.GetReplicationStatusResponse> getReplicationStatus(
      $pb.ServerContext ctx, $2.GetReplicationStatusRequest request);
  $async.Future<$2.BackupSnapshotResponse> backupSnapshot(
      $pb.ServerContext ctx, $2.BackupSnapshotRequest request);

  $pb.GeneratedMessage createRequest($core.String methodName) {
    switch (methodName) {
      case 'Illuminate':
        return $2.IlluminateRequest();
      case 'GetVertex':
        return $2.GetVertexRequest();
      case 'GetVertices':
        return $2.GetVerticesRequest();
      case 'PutVertex':
        return $2.PutVertexRequest();
      case 'PutVertices':
        return $2.PutVerticesRequest();
      case 'DeleteVertex':
        return $2.DeleteVertexRequest();
      case 'DeleteVertices':
        return $2.DeleteVerticesRequest();
      case 'ScanVertices':
        return $2.ScanVerticesRequest();
      case 'ScanVertexKeys':
        return $2.ScanVertexKeysRequest();
      case 'SearchVertices':
        return $2.SearchVerticesRequest();
      case 'CountVerticesByPrefix':
        return $2.CountVerticesByPrefixRequest();
      case 'DeleteVerticesByPrefix':
        return $2.DeleteVerticesByPrefixRequest();
      case 'TopVerticesByDegree':
        return $2.TopVerticesByDegreeRequest();
      case 'GetEdge':
        return $2.GetEdgeRequest();
      case 'GetEdges':
        return $2.GetEdgesRequest();
      case 'AddEdge':
        return $2.AddEdgeRequest();
      case 'AddEdges':
        return $2.AddEdgesRequest();
      case 'PutEdge':
        return $2.PutEdgeRequest();
      case 'PutEdges':
        return $2.PutEdgesRequest();
      case 'DeleteEdge':
        return $2.DeleteEdgeRequest();
      case 'DeleteEdges':
        return $2.DeleteEdgesRequest();
      case 'DeleteEdgesByPrefix':
        return $2.DeleteEdgesByPrefixRequest();
      case 'ScanEdges':
        return $2.ScanEdgesRequest();
      case 'GetServerStatus':
        return $2.GetServerStatusRequest();
      case 'GetReplicationStatus':
        return $2.GetReplicationStatusRequest();
      case 'BackupSnapshot':
        return $2.BackupSnapshotRequest();
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $async.Future<$pb.GeneratedMessage> handleCall($pb.ServerContext ctx,
      $core.String methodName, $pb.GeneratedMessage request) {
    switch (methodName) {
      case 'Illuminate':
        return illuminate(ctx, request as $2.IlluminateRequest);
      case 'GetVertex':
        return getVertex(ctx, request as $2.GetVertexRequest);
      case 'GetVertices':
        return getVertices(ctx, request as $2.GetVerticesRequest);
      case 'PutVertex':
        return putVertex(ctx, request as $2.PutVertexRequest);
      case 'PutVertices':
        return putVertices(ctx, request as $2.PutVerticesRequest);
      case 'DeleteVertex':
        return deleteVertex(ctx, request as $2.DeleteVertexRequest);
      case 'DeleteVertices':
        return deleteVertices(ctx, request as $2.DeleteVerticesRequest);
      case 'ScanVertices':
        return scanVertices(ctx, request as $2.ScanVerticesRequest);
      case 'ScanVertexKeys':
        return scanVertexKeys(ctx, request as $2.ScanVertexKeysRequest);
      case 'SearchVertices':
        return searchVertices(ctx, request as $2.SearchVerticesRequest);
      case 'CountVerticesByPrefix':
        return countVerticesByPrefix(
            ctx, request as $2.CountVerticesByPrefixRequest);
      case 'DeleteVerticesByPrefix':
        return deleteVerticesByPrefix(
            ctx, request as $2.DeleteVerticesByPrefixRequest);
      case 'TopVerticesByDegree':
        return topVerticesByDegree(
            ctx, request as $2.TopVerticesByDegreeRequest);
      case 'GetEdge':
        return getEdge(ctx, request as $2.GetEdgeRequest);
      case 'GetEdges':
        return getEdges(ctx, request as $2.GetEdgesRequest);
      case 'AddEdge':
        return addEdge(ctx, request as $2.AddEdgeRequest);
      case 'AddEdges':
        return addEdges(ctx, request as $2.AddEdgesRequest);
      case 'PutEdge':
        return putEdge(ctx, request as $2.PutEdgeRequest);
      case 'PutEdges':
        return putEdges(ctx, request as $2.PutEdgesRequest);
      case 'DeleteEdge':
        return deleteEdge(ctx, request as $2.DeleteEdgeRequest);
      case 'DeleteEdges':
        return deleteEdges(ctx, request as $2.DeleteEdgesRequest);
      case 'DeleteEdgesByPrefix':
        return deleteEdgesByPrefix(
            ctx, request as $2.DeleteEdgesByPrefixRequest);
      case 'ScanEdges':
        return scanEdges(ctx, request as $2.ScanEdgesRequest);
      case 'GetServerStatus':
        return getServerStatus(ctx, request as $2.GetServerStatusRequest);
      case 'GetReplicationStatus':
        return getReplicationStatus(
            ctx, request as $2.GetReplicationStatusRequest);
      case 'BackupSnapshot':
        return backupSnapshot(ctx, request as $2.BackupSnapshotRequest);
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $core.Map<$core.String, $core.dynamic> get $json => LanternServiceBase$json;
  $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
      get $messageJson => LanternServiceBase$messageJson;
}
