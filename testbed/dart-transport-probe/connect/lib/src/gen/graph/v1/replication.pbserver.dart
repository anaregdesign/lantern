// This is a generated file - do not edit.
//
// Generated from graph/v1/replication.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

import 'replication.pb.dart' as $3;
import 'replication.pbjson.dart';

export 'replication.pb.dart';

abstract class LanternReplicationServiceBase extends $pb.GeneratedService {
  $async.Future<$3.SubscribeResponse> subscribe(
      $pb.ServerContext ctx, $3.SubscribeRequest request);
  $async.Future<$3.SnapshotResponse> snapshot(
      $pb.ServerContext ctx, $3.SnapshotRequest request);
  $async.Future<$3.PeerStatusResponse> peerStatus(
      $pb.ServerContext ctx, $3.PeerStatusRequest request);

  $pb.GeneratedMessage createRequest($core.String methodName) {
    switch (methodName) {
      case 'Subscribe':
        return $3.SubscribeRequest();
      case 'Snapshot':
        return $3.SnapshotRequest();
      case 'PeerStatus':
        return $3.PeerStatusRequest();
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $async.Future<$pb.GeneratedMessage> handleCall($pb.ServerContext ctx,
      $core.String methodName, $pb.GeneratedMessage request) {
    switch (methodName) {
      case 'Subscribe':
        return subscribe(ctx, request as $3.SubscribeRequest);
      case 'Snapshot':
        return snapshot(ctx, request as $3.SnapshotRequest);
      case 'PeerStatus':
        return peerStatus(ctx, request as $3.PeerStatusRequest);
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $core.Map<$core.String, $core.dynamic> get $json =>
      LanternReplicationServiceBase$json;
  $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
      get $messageJson => LanternReplicationServiceBase$messageJson;
}
