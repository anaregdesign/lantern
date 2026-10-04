// This is a generated file - do not edit.
//
// Generated from graph/v1/security_peer.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

import 'security_peer.pb.dart' as $0;
import 'security_peer.pbjson.dart';

export 'security_peer.pb.dart';

abstract class LanternSecurityPeerServiceBase extends $pb.GeneratedService {
  $async.Future<$0.RenewPolicyLeaseResponse> renewPolicyLease(
      $pb.ServerContext ctx, $0.RenewPolicyLeaseRequest request);

  $pb.GeneratedMessage createRequest($core.String methodName) {
    switch (methodName) {
      case 'RenewPolicyLease':
        return $0.RenewPolicyLeaseRequest();
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $async.Future<$pb.GeneratedMessage> handleCall($pb.ServerContext ctx,
      $core.String methodName, $pb.GeneratedMessage request) {
    switch (methodName) {
      case 'RenewPolicyLease':
        return renewPolicyLease(ctx, request as $0.RenewPolicyLeaseRequest);
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $core.Map<$core.String, $core.dynamic> get $json =>
      LanternSecurityPeerServiceBase$json;
  $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
      get $messageJson => LanternSecurityPeerServiceBase$messageJson;
}
