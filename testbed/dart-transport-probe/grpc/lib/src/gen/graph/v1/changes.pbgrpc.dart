// This is a generated file - do not edit.
//
// Generated from graph/v1/changes.proto.

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

import 'changes.pb.dart' as $0;

export 'changes.pb.dart';

/// Public CDC never exposes the private replication protocol. Every event is an
/// invalidation of an exact committed identity. Value mode requires explicit
/// identity/value CDC and data-read grants and additionally samples
/// the current local live value; it is not a causally ordered event-sourcing log.
@$pb.GrpcServiceName('graph.v1.LanternChangeService')
class LanternChangeServiceClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  LanternChangeServiceClient(super.channel,
      {super.options, super.interceptors});

  $grpc.ResponseStream<$0.WatchChangesResponse> watchChanges(
    $0.WatchChangesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createStreamingCall(
        _$watchChanges, $async.Stream.fromIterable([request]),
        options: options);
  }

  // method descriptors

  static final _$watchChanges =
      $grpc.ClientMethod<$0.WatchChangesRequest, $0.WatchChangesResponse>(
          '/graph.v1.LanternChangeService/WatchChanges',
          ($0.WatchChangesRequest value) => value.writeToBuffer(),
          $0.WatchChangesResponse.fromBuffer);
}

@$pb.GrpcServiceName('graph.v1.LanternChangeService')
abstract class LanternChangeServiceBase extends $grpc.Service {
  $core.String get $name => 'graph.v1.LanternChangeService';

  LanternChangeServiceBase() {
    $addMethod(
        $grpc.ServiceMethod<$0.WatchChangesRequest, $0.WatchChangesResponse>(
            'WatchChanges',
            watchChanges_Pre,
            false,
            true,
            ($core.List<$core.int> value) =>
                $0.WatchChangesRequest.fromBuffer(value),
            ($0.WatchChangesResponse value) => value.writeToBuffer()));
  }

  $async.Stream<$0.WatchChangesResponse> watchChanges_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.WatchChangesRequest> $request) async* {
    yield* watchChanges($call, await $request);
  }

  $async.Stream<$0.WatchChangesResponse> watchChanges(
      $grpc.ServiceCall call, $0.WatchChangesRequest request);
}
