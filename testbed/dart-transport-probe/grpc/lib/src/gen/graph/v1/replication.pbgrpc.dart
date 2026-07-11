// This is a generated file - do not edit.
//
// Generated from graph/v1/replication.proto.

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

import 'replication.pb.dart' as $0;

export 'replication.pb.dart';

/// LanternReplicationService carries the peer-to-peer (and CDC) replication
/// surface. It is intentionally a separate service from LanternService so
/// that operators can route, throttle, secure, or even disable replication
/// independently of the public read/write API.
///
/// NOTE — deviation from the original RFC: the Subscribe RPC was originally
/// specified to live on LanternService. The realised proto split introduces
/// a cyclic file import between graph.proto and replication.proto when both
/// the request/response messages and the RPC live on opposite sides of the
/// boundary. Hosting Subscribe on a dedicated service keeps the import
/// graph one-way (replication.proto → graph.proto) and is documented as
/// part of #178's implementation in docs/replication.md.
@$pb.GrpcServiceName('graph.v1.LanternReplicationService')
class LanternReplicationServiceClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  LanternReplicationServiceClient(super.channel,
      {super.options, super.interceptors});

  /// Subscribe streams replicated mutations to a peer (or CDC consumer)
  /// starting at `from_seq` (inclusive). The server replays any in-buffer
  /// entries first, then streams live mutations as they are appended.
  ///
  /// If `from_seq` is below the server's first available seq the call
  /// fails with FAILED_PRECONDITION ("gapped") and the caller must
  /// snapshot + resubscribe. Slow consumers whose channel backs up may
  /// also have the stream terminated with FAILED_PRECONDITION — the
  /// remedy is the same.
  ///
  /// No HTTP gateway annotation: replication is intentionally exposed
  /// only as Connect server-streaming (consumed by replication pumps on
  /// peer nodes, never directly from browsers).
  $grpc.ResponseStream<$0.SubscribeResponse> subscribe(
    $0.SubscribeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createStreamingCall(
        _$subscribe, $async.Stream.fromIterable([request]),
        options: options);
  }

  /// Snapshot streams a point-in-time, causally-consistent dump of every
  /// live vertex and edge to a bootstrapping peer. The first frame is a
  /// SnapshotHeader carrying the (cutoff_seq_per_origin, cutoff_hlc) the
  /// server used to materialise the snapshot; the last frame is a
  /// SnapshotFooter with the actual vertex / edge counts streamed.
  ///
  /// Bootstrap stitch contract: after receiving the SnapshotFooter the
  /// peer MUST call `Subscribe(from_seq_per_origin = {origin: seq+1 for
  /// each (origin, seq) in cutoff_seq_per_origin})` to pick up the live
  /// tail. Without that the snapshot and the live stream cannot be glued
  /// together without gap or overlap.
  ///
  /// No HTTP gateway annotation (parity with Subscribe).
  $grpc.ResponseStream<$0.SnapshotResponse> snapshot(
    $0.SnapshotRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createStreamingCall(
        _$snapshot, $async.Stream.fromIterable([request]),
        options: options);
  }

  /// PeerStatus returns the responder's per-origin (last_applied_seq,
  /// last_applied_hlc) map. Used by anti-entropy (#186) as the
  /// convergence safety net for the Subscribe pump: a periodic
  /// PeerStatus poll lets each node detect that the peer's view of
  /// some origin is ahead of its own and request the missing tail
  /// (via Subscribe(from_seq = local+1)) or — when that returns
  /// FailedPrecondition — fall back to Snapshot.
  ///
  /// The reply set always includes the responder's OWN origin (taken
  /// from its mutation log's last appended seq + hlc) plus every
  /// remote origin whose mutation has ever been applied via
  /// ApplyMutation since process start. Origins the responder has
  /// never seen are simply absent from the map.
  ///
  /// The RPC is intentionally unary and stateless — callers can
  /// multiplex it across many peers without holding any server-side
  /// resources. Returns Unavailable when the mutation log is unset
  /// (single-instance test path).
  $grpc.ResponseFuture<$0.PeerStatusResponse> peerStatus(
    $0.PeerStatusRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$peerStatus, request, options: options);
  }

  // method descriptors

  static final _$subscribe =
      $grpc.ClientMethod<$0.SubscribeRequest, $0.SubscribeResponse>(
          '/graph.v1.LanternReplicationService/Subscribe',
          ($0.SubscribeRequest value) => value.writeToBuffer(),
          $0.SubscribeResponse.fromBuffer);
  static final _$snapshot =
      $grpc.ClientMethod<$0.SnapshotRequest, $0.SnapshotResponse>(
          '/graph.v1.LanternReplicationService/Snapshot',
          ($0.SnapshotRequest value) => value.writeToBuffer(),
          $0.SnapshotResponse.fromBuffer);
  static final _$peerStatus =
      $grpc.ClientMethod<$0.PeerStatusRequest, $0.PeerStatusResponse>(
          '/graph.v1.LanternReplicationService/PeerStatus',
          ($0.PeerStatusRequest value) => value.writeToBuffer(),
          $0.PeerStatusResponse.fromBuffer);
}

@$pb.GrpcServiceName('graph.v1.LanternReplicationService')
abstract class LanternReplicationServiceBase extends $grpc.Service {
  $core.String get $name => 'graph.v1.LanternReplicationService';

  LanternReplicationServiceBase() {
    $addMethod($grpc.ServiceMethod<$0.SubscribeRequest, $0.SubscribeResponse>(
        'Subscribe',
        subscribe_Pre,
        false,
        true,
        ($core.List<$core.int> value) => $0.SubscribeRequest.fromBuffer(value),
        ($0.SubscribeResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.SnapshotRequest, $0.SnapshotResponse>(
        'Snapshot',
        snapshot_Pre,
        false,
        true,
        ($core.List<$core.int> value) => $0.SnapshotRequest.fromBuffer(value),
        ($0.SnapshotResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.PeerStatusRequest, $0.PeerStatusResponse>(
        'PeerStatus',
        peerStatus_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.PeerStatusRequest.fromBuffer(value),
        ($0.PeerStatusResponse value) => value.writeToBuffer()));
  }

  $async.Stream<$0.SubscribeResponse> subscribe_Pre($grpc.ServiceCall $call,
      $async.Future<$0.SubscribeRequest> $request) async* {
    yield* subscribe($call, await $request);
  }

  $async.Stream<$0.SubscribeResponse> subscribe(
      $grpc.ServiceCall call, $0.SubscribeRequest request);

  $async.Stream<$0.SnapshotResponse> snapshot_Pre($grpc.ServiceCall $call,
      $async.Future<$0.SnapshotRequest> $request) async* {
    yield* snapshot($call, await $request);
  }

  $async.Stream<$0.SnapshotResponse> snapshot(
      $grpc.ServiceCall call, $0.SnapshotRequest request);

  $async.Future<$0.PeerStatusResponse> peerStatus_Pre($grpc.ServiceCall $call,
      $async.Future<$0.PeerStatusRequest> $request) async {
    return peerStatus($call, await $request);
  }

  $async.Future<$0.PeerStatusResponse> peerStatus(
      $grpc.ServiceCall call, $0.PeerStatusRequest request);
}
