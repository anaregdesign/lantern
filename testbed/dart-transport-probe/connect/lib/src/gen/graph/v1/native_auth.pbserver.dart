// This is a generated file - do not edit.
//
// Generated from graph/v1/native_auth.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

import 'native_auth.pb.dart' as $2;
import 'native_auth.pbjson.dart';

export 'native_auth.pb.dart';

abstract class LanternNativeAuthServiceBase extends $pb.GeneratedService {
  $async.Future<$2.BeginNativeLoginResponse> beginNativeLogin(
      $pb.ServerContext ctx, $2.BeginNativeLoginRequest request);
  $async.Future<$2.CompleteNativeLoginResponse> completeNativeLogin(
      $pb.ServerContext ctx, $2.CompleteNativeLoginRequest request);
  $async.Future<$2.GetNativeSessionResponse> getNativeSession(
      $pb.ServerContext ctx, $2.GetNativeSessionRequest request);
  $async.Future<$2.PrepareNativeLogoutResponse> prepareNativeLogout(
      $pb.ServerContext ctx, $2.PrepareNativeLogoutRequest request);
  $async.Future<$2.ApplyNativeLogoutResponse> applyNativeLogout(
      $pb.ServerContext ctx, $2.ApplyNativeLogoutRequest request);

  $pb.GeneratedMessage createRequest($core.String methodName) {
    switch (methodName) {
      case 'BeginNativeLogin':
        return $2.BeginNativeLoginRequest();
      case 'CompleteNativeLogin':
        return $2.CompleteNativeLoginRequest();
      case 'GetNativeSession':
        return $2.GetNativeSessionRequest();
      case 'PrepareNativeLogout':
        return $2.PrepareNativeLogoutRequest();
      case 'ApplyNativeLogout':
        return $2.ApplyNativeLogoutRequest();
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $async.Future<$pb.GeneratedMessage> handleCall($pb.ServerContext ctx,
      $core.String methodName, $pb.GeneratedMessage request) {
    switch (methodName) {
      case 'BeginNativeLogin':
        return beginNativeLogin(ctx, request as $2.BeginNativeLoginRequest);
      case 'CompleteNativeLogin':
        return completeNativeLogin(
            ctx, request as $2.CompleteNativeLoginRequest);
      case 'GetNativeSession':
        return getNativeSession(ctx, request as $2.GetNativeSessionRequest);
      case 'PrepareNativeLogout':
        return prepareNativeLogout(
            ctx, request as $2.PrepareNativeLogoutRequest);
      case 'ApplyNativeLogout':
        return applyNativeLogout(ctx, request as $2.ApplyNativeLogoutRequest);
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $core.Map<$core.String, $core.dynamic> get $json =>
      LanternNativeAuthServiceBase$json;
  $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
      get $messageJson => LanternNativeAuthServiceBase$messageJson;
}
