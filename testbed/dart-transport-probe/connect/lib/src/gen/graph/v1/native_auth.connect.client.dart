//
//  Generated code. Do not modify.
//  source: graph/v1/native_auth.proto
//

import "package:connectrpc/connect.dart" as connect;
import "native_auth.pb.dart" as graphv1native_auth;
import "native_auth.connect.spec.dart" as specs;

/// Native provider inputs are accepted only here. Ordinary RPC authentication
/// uses the returned opaque native credential through the existing Bearer path.
/// These unary methods are never automatically retried after possible dispatch.
extension type LanternNativeAuthServiceClient (connect.Transport _transport) {
  Future<graphv1native_auth.BeginNativeLoginResponse> beginNativeLogin(
    graphv1native_auth.BeginNativeLoginRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternNativeAuthService.beginNativeLogin,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1native_auth.CompleteNativeLoginResponse> completeNativeLogin(
    graphv1native_auth.CompleteNativeLoginRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternNativeAuthService.completeNativeLogin,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1native_auth.GetNativeSessionResponse> getNativeSession(
    graphv1native_auth.GetNativeSessionRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternNativeAuthService.getNativeSession,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1native_auth.PrepareNativeLogoutResponse> prepareNativeLogout(
    graphv1native_auth.PrepareNativeLogoutRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternNativeAuthService.prepareNativeLogout,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1native_auth.ApplyNativeLogoutResponse> applyNativeLogout(
    graphv1native_auth.ApplyNativeLogoutRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternNativeAuthService.applyNativeLogout,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }
}
