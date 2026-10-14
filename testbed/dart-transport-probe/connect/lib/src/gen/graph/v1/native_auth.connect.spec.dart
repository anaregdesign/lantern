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

  static const beginNativeRegistration = connect.Spec(
    '/$name/BeginNativeRegistration',
    connect.StreamType.unary,
    graphv1native_auth.BeginNativeRegistrationRequest.new,
    graphv1native_auth.BeginNativeRegistrationResponse.new,
  );

  static const prepareNativeRegistration = connect.Spec(
    '/$name/PrepareNativeRegistration',
    connect.StreamType.unary,
    graphv1native_auth.PrepareNativeRegistrationRequest.new,
    graphv1native_auth.PrepareNativeRegistrationResponse.new,
  );

  static const applyNativeRegistration = connect.Spec(
    '/$name/ApplyNativeRegistration',
    connect.StreamType.unary,
    graphv1native_auth.ApplyNativeRegistrationRequest.new,
    graphv1native_auth.ApplyNativeRegistrationResponse.new,
  );

  static const getNativeRegistrationStatus = connect.Spec(
    '/$name/GetNativeRegistrationStatus',
    connect.StreamType.unary,
    graphv1native_auth.GetNativeRegistrationStatusRequest.new,
    graphv1native_auth.GetNativeRegistrationStatusResponse.new,
  );
}
