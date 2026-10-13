// This is a generated file - do not edit.
//
// Generated from graph/v1/native_auth.proto.

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

import 'native_auth.pb.dart' as $0;

export 'native_auth.pb.dart';

/// Native provider inputs are accepted only here. Ordinary RPC authentication
/// uses the returned opaque native credential through the existing Bearer path.
/// These unary methods are never automatically retried after possible dispatch.
@$pb.GrpcServiceName('graph.v1.LanternNativeAuthService')
class LanternNativeAuthServiceClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  LanternNativeAuthServiceClient(super.channel,
      {super.options, super.interceptors});

  $grpc.ResponseFuture<$0.BeginNativeLoginResponse> beginNativeLogin(
    $0.BeginNativeLoginRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$beginNativeLogin, request, options: options);
  }

  $grpc.ResponseFuture<$0.CompleteNativeLoginResponse> completeNativeLogin(
    $0.CompleteNativeLoginRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$completeNativeLogin, request, options: options);
  }

  $grpc.ResponseFuture<$0.GetNativeSessionResponse> getNativeSession(
    $0.GetNativeSessionRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getNativeSession, request, options: options);
  }

  $grpc.ResponseFuture<$0.PrepareNativeLogoutResponse> prepareNativeLogout(
    $0.PrepareNativeLogoutRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$prepareNativeLogout, request, options: options);
  }

  $grpc.ResponseFuture<$0.ApplyNativeLogoutResponse> applyNativeLogout(
    $0.ApplyNativeLogoutRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$applyNativeLogout, request, options: options);
  }

  // method descriptors

  static final _$beginNativeLogin = $grpc.ClientMethod<
          $0.BeginNativeLoginRequest, $0.BeginNativeLoginResponse>(
      '/graph.v1.LanternNativeAuthService/BeginNativeLogin',
      ($0.BeginNativeLoginRequest value) => value.writeToBuffer(),
      $0.BeginNativeLoginResponse.fromBuffer);
  static final _$completeNativeLogin = $grpc.ClientMethod<
          $0.CompleteNativeLoginRequest, $0.CompleteNativeLoginResponse>(
      '/graph.v1.LanternNativeAuthService/CompleteNativeLogin',
      ($0.CompleteNativeLoginRequest value) => value.writeToBuffer(),
      $0.CompleteNativeLoginResponse.fromBuffer);
  static final _$getNativeSession = $grpc.ClientMethod<
          $0.GetNativeSessionRequest, $0.GetNativeSessionResponse>(
      '/graph.v1.LanternNativeAuthService/GetNativeSession',
      ($0.GetNativeSessionRequest value) => value.writeToBuffer(),
      $0.GetNativeSessionResponse.fromBuffer);
  static final _$prepareNativeLogout = $grpc.ClientMethod<
          $0.PrepareNativeLogoutRequest, $0.PrepareNativeLogoutResponse>(
      '/graph.v1.LanternNativeAuthService/PrepareNativeLogout',
      ($0.PrepareNativeLogoutRequest value) => value.writeToBuffer(),
      $0.PrepareNativeLogoutResponse.fromBuffer);
  static final _$applyNativeLogout = $grpc.ClientMethod<
          $0.ApplyNativeLogoutRequest, $0.ApplyNativeLogoutResponse>(
      '/graph.v1.LanternNativeAuthService/ApplyNativeLogout',
      ($0.ApplyNativeLogoutRequest value) => value.writeToBuffer(),
      $0.ApplyNativeLogoutResponse.fromBuffer);
}

@$pb.GrpcServiceName('graph.v1.LanternNativeAuthService')
abstract class LanternNativeAuthServiceBase extends $grpc.Service {
  $core.String get $name => 'graph.v1.LanternNativeAuthService';

  LanternNativeAuthServiceBase() {
    $addMethod($grpc.ServiceMethod<$0.BeginNativeLoginRequest,
            $0.BeginNativeLoginResponse>(
        'BeginNativeLogin',
        beginNativeLogin_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.BeginNativeLoginRequest.fromBuffer(value),
        ($0.BeginNativeLoginResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.CompleteNativeLoginRequest,
            $0.CompleteNativeLoginResponse>(
        'CompleteNativeLogin',
        completeNativeLogin_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.CompleteNativeLoginRequest.fromBuffer(value),
        ($0.CompleteNativeLoginResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetNativeSessionRequest,
            $0.GetNativeSessionResponse>(
        'GetNativeSession',
        getNativeSession_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.GetNativeSessionRequest.fromBuffer(value),
        ($0.GetNativeSessionResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.PrepareNativeLogoutRequest,
            $0.PrepareNativeLogoutResponse>(
        'PrepareNativeLogout',
        prepareNativeLogout_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.PrepareNativeLogoutRequest.fromBuffer(value),
        ($0.PrepareNativeLogoutResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ApplyNativeLogoutRequest,
            $0.ApplyNativeLogoutResponse>(
        'ApplyNativeLogout',
        applyNativeLogout_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ApplyNativeLogoutRequest.fromBuffer(value),
        ($0.ApplyNativeLogoutResponse value) => value.writeToBuffer()));
  }

  $async.Future<$0.BeginNativeLoginResponse> beginNativeLogin_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.BeginNativeLoginRequest> $request) async {
    return beginNativeLogin($call, await $request);
  }

  $async.Future<$0.BeginNativeLoginResponse> beginNativeLogin(
      $grpc.ServiceCall call, $0.BeginNativeLoginRequest request);

  $async.Future<$0.CompleteNativeLoginResponse> completeNativeLogin_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.CompleteNativeLoginRequest> $request) async {
    return completeNativeLogin($call, await $request);
  }

  $async.Future<$0.CompleteNativeLoginResponse> completeNativeLogin(
      $grpc.ServiceCall call, $0.CompleteNativeLoginRequest request);

  $async.Future<$0.GetNativeSessionResponse> getNativeSession_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.GetNativeSessionRequest> $request) async {
    return getNativeSession($call, await $request);
  }

  $async.Future<$0.GetNativeSessionResponse> getNativeSession(
      $grpc.ServiceCall call, $0.GetNativeSessionRequest request);

  $async.Future<$0.PrepareNativeLogoutResponse> prepareNativeLogout_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.PrepareNativeLogoutRequest> $request) async {
    return prepareNativeLogout($call, await $request);
  }

  $async.Future<$0.PrepareNativeLogoutResponse> prepareNativeLogout(
      $grpc.ServiceCall call, $0.PrepareNativeLogoutRequest request);

  $async.Future<$0.ApplyNativeLogoutResponse> applyNativeLogout_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ApplyNativeLogoutRequest> $request) async {
    return applyNativeLogout($call, await $request);
  }

  $async.Future<$0.ApplyNativeLogoutResponse> applyNativeLogout(
      $grpc.ServiceCall call, $0.ApplyNativeLogoutRequest request);
}
