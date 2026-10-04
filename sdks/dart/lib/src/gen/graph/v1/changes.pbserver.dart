// This is a generated file - do not edit.
//
// Generated from graph/v1/changes.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

import 'changes.pb.dart' as $3;
import 'changes.pbjson.dart';

export 'changes.pb.dart';

abstract class LanternChangeServiceBase extends $pb.GeneratedService {
  $async.Future<$3.WatchChangesResponse> watchChanges(
      $pb.ServerContext ctx, $3.WatchChangesRequest request);

  $pb.GeneratedMessage createRequest($core.String methodName) {
    switch (methodName) {
      case 'WatchChanges':
        return $3.WatchChangesRequest();
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $async.Future<$pb.GeneratedMessage> handleCall($pb.ServerContext ctx,
      $core.String methodName, $pb.GeneratedMessage request) {
    switch (methodName) {
      case 'WatchChanges':
        return watchChanges(ctx, request as $3.WatchChangesRequest);
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $core.Map<$core.String, $core.dynamic> get $json =>
      LanternChangeServiceBase$json;
  $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
      get $messageJson => LanternChangeServiceBase$messageJson;
}
