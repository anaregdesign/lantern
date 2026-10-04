// This is a generated file - do not edit.
//
// Generated from graph/v1/security_peer.proto.

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

import 'security_peer.pb.dart' as $0;

export 'security_peer.pb.dart';

/// Private mTLS workload plane only. This service is never mounted on the
/// public data/browser listener, including in OFF mode.
@$pb.GrpcServiceName('graph.v1.LanternSecurityPeerService')
class LanternSecurityPeerServiceClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  LanternSecurityPeerServiceClient(super.channel,
      {super.options, super.interceptors});

  $grpc.ResponseFuture<$0.RenewPolicyLeaseResponse> renewPolicyLease(
    $0.RenewPolicyLeaseRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$renewPolicyLease, request, options: options);
  }

  // method descriptors

  static final _$renewPolicyLease = $grpc.ClientMethod<
          $0.RenewPolicyLeaseRequest, $0.RenewPolicyLeaseResponse>(
      '/graph.v1.LanternSecurityPeerService/RenewPolicyLease',
      ($0.RenewPolicyLeaseRequest value) => value.writeToBuffer(),
      $0.RenewPolicyLeaseResponse.fromBuffer);
}

@$pb.GrpcServiceName('graph.v1.LanternSecurityPeerService')
abstract class LanternSecurityPeerServiceBase extends $grpc.Service {
  $core.String get $name => 'graph.v1.LanternSecurityPeerService';

  LanternSecurityPeerServiceBase() {
    $addMethod($grpc.ServiceMethod<$0.RenewPolicyLeaseRequest,
            $0.RenewPolicyLeaseResponse>(
        'RenewPolicyLease',
        renewPolicyLease_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.RenewPolicyLeaseRequest.fromBuffer(value),
        ($0.RenewPolicyLeaseResponse value) => value.writeToBuffer()));
  }

  $async.Future<$0.RenewPolicyLeaseResponse> renewPolicyLease_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.RenewPolicyLeaseRequest> $request) async {
    return renewPolicyLease($call, await $request);
  }

  $async.Future<$0.RenewPolicyLeaseResponse> renewPolicyLease(
      $grpc.ServiceCall call, $0.RenewPolicyLeaseRequest request);
}
