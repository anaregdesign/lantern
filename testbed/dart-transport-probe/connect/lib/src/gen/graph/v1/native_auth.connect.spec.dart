//
//  Generated code. Do not modify.
//  source: graph/v1/native_auth.proto
//

import "package:connectrpc/connect.dart" as connect;
import "native_auth.pb.dart" as graphv1native_auth;

/// Native provider inputs are accepted only here. Ordinary RPC authentication
/// uses the returned opaque native credential through the existing Bearer path.
/// These unary methods are never automatically retried after possible dispatch.
abstract final class LanternNativeAuthService {
  /// Fully-qualified name of the LanternNativeAuthService service.
  static const name = 'graph.v1.LanternNativeAuthService';

  static const beginNativeLogin = connect.Spec(
    '/$name/BeginNativeLogin',
    connect.StreamType.unary,
    graphv1native_auth.BeginNativeLoginRequest.new,
    graphv1native_auth.BeginNativeLoginResponse.new,
  );

  static const completeNativeLogin = connect.Spec(
    '/$name/CompleteNativeLogin',
    connect.StreamType.unary,
    graphv1native_auth.CompleteNativeLoginRequest.new,
    graphv1native_auth.CompleteNativeLoginResponse.new,
  );

  static const getNativeSession = connect.Spec(
    '/$name/GetNativeSession',
    connect.StreamType.unary,
    graphv1native_auth.GetNativeSessionRequest.new,
    graphv1native_auth.GetNativeSessionResponse.new,
  );

  static const prepareNativeLogout = connect.Spec(
    '/$name/PrepareNativeLogout',
    connect.StreamType.unary,
    graphv1native_auth.PrepareNativeLogoutRequest.new,
    graphv1native_auth.PrepareNativeLogoutResponse.new,
  );

  static const applyNativeLogout = connect.Spec(
    '/$name/ApplyNativeLogout',
    connect.StreamType.unary,
    graphv1native_auth.ApplyNativeLogoutRequest.new,
    graphv1native_auth.ApplyNativeLogoutResponse.new,
  );
}
