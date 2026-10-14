part of 'client.dart';

/// Closed provider/client profiles supported by the native login service.
enum NativeProviderKind {
  /// Google backend-audience ID token; no per-login nonce or freshness claim.
  google,

  /// Apple Bundle/App ID, raw nonce, and one server-owned code exchange.
  appleNative,

  /// Apple Services ID and registered Android HTTPS form-post callback.
  appleAndroidWeb,
}

/// Trusted plugin inputs returned by the configured Lantern endpoint.
final class NativePluginConfiguration {
  NativePluginConfiguration._(
    this.kind,
    this.clientProfileId,
    this.clientId,
    this.serviceOrigin,
    this.redirectUri,
    this.nonce,
    this.state,
  );

  /// Registered platform/provider profile.
  final NativeProviderKind kind;

  /// Stable operator-selected profile identifier.
  final String clientProfileId;

  /// Google backend Web client, Apple Bundle/App ID, or Apple Services ID.
  final String clientId;

  /// Exact configured public HTTPS service origin.
  final Uri serviceOrigin;

  /// Registered Android web callback, absent for native Apple and Google.
  final Uri? redirectUri;

  /// Raw Apple nonce. Google initialization must use no nonce.
  final String? nonce;

  /// Independent Apple transaction state, including an unauthenticated affinity hint.
  final String? state;
  @override
  String toString() => 'NativePluginConfiguration(${kind.name}, redacted)';
}

/// Ephemeral login proof bound to one endpoint. Never persist or log this value.
final class NativeLoginAttempt {
  NativeLoginAttempt._(
    this._endpoint,
    this._attempt,
    this._secret,
    this.expiresAt,
    this.affinity,
    this.configuration,
  );
  final Uri _endpoint;
  final String _attempt, _secret;

  /// Absolute transaction expiry; not a session or authentication timestamp.
  final DateTime expiresAt;

  /// Routing hint only. It does not authenticate a client or provider callback.
  final String affinity;

  /// App-owned plugin configuration for this attempt.
  final NativePluginConfiguration configuration;
  @override
  String toString() => 'NativeLoginAttempt(redacted)';
}

/// Provider credential supplied by the application-owned plugin.
sealed class NativeProviderCredential {
  const NativeProviderCredential();
  @override
  String toString() => 'NativeProviderCredential(redacted)';
}

/// Google ID token. Provider access tokens and serverAuthCode are not required.
final class GoogleNativeCredential extends NativeProviderCredential {
  /// Captures a provider ID token without parsing its untrusted identity.
  const GoogleNativeCredential({required String idToken}) : _idToken = idToken;
  final String _idToken;
}

/// Apple identity token, one-time authorization code and returned state.
final class AppleNativeCredential extends NativeProviderCredential {
  /// Captures the plugin result. Only the Server verifies its authentication.
  const AppleNativeCredential({
    required String identityToken,
    required String authorizationCode,
    required String state,
  }) : _identityToken = identityToken,
       _code = authorizationCode,
       _state = state;
  final String _identityToken, _code, _state;
}

/// Exact verified Principal. Email and plugin-supplied account IDs are not identity.
final class NativePrincipal {
  NativePrincipal._(this.issuer, this.subject);

  /// Exact verified issuer spelling.
  final String issuer;

  /// Exact verified subject; do not normalize or link it by email.
  final String subject;
}

/// Non-secret metadata for a fixed-expiry native end-user session.
final class NativeSession {
  NativeSession._(
    this.principal,
    this.clientProfileId,
    this.serviceOrigin,
    this.createdAt,
    this.expiresAt,
    this.authTime,
    this.authorityBinding,
    this.userId,
    this.personalPrefix,
  );

  /// Existing registered Principal selected by the Server.
  final NativePrincipal principal;

  /// Stable native client profile.
  final String clientProfileId;

  /// Trusted public service origin; matched to this client's endpoint.
  final Uri serviceOrigin;

  /// Session creation, never a replacement for signed authTime.
  final DateTime createdAt;

  /// Absolute expiry. Current policy/revocation may stop it sooner.
  final DateTime expiresAt;

  /// Original signed authentication time, or null when unknown.
  final DateTime? authTime;

  /// Current profile/cut/credential binding for active cache ownership only.
  final String authorityBinding;

  /// Immutable installed UID; null for a legacy user who has not registered.
  final String? userId;

  /// Logical personal root, including its trailing colon. Never prepend data:.
  /// This is metadata; each data operation still requires current authorization.
  final String? personalPrefix;
}

/// Login result. Store its credential only in application-owned secure storage.
final class NativeSessionCredential {
  NativeSessionCredential._(this.credential, this.session);

  /// Opaque Lantern credential for the existing TokenProvider.
  final String credential;

  /// Verified response metadata. This is not a cached authorization grant.
  final NativeSession session;
  @override
  String toString() => 'NativeSessionCredential(redacted)';
}

/// Current role display metadata, not permission to perform a future operation.
final class NativeRole {
  NativeRole._(this.id, this.name);

  /// Registered role identifier.
  final String id;

  /// Operator-provided display name.
  final String name;
}

/// Authenticated session inspection under the current authority.
final class NativeSessionView {
  NativeSessionView._(this.session, this.roles);

  /// Current session and authority binding.
  final NativeSession session;

  /// Immutable current role display metadata.
  final List<NativeRole> roles;
}

/// A retained, endpoint-bound logout identity. Persist before dispatching Apply.
/// It contains no credential and cannot itself authorize revocation.
final class NativeLogoutReview {
  NativeLogoutReview._(this._endpoint, this._wire);
  final Uri _endpoint;
  final $security.CurrentSessionRevocationReview _wire;

  /// Restores the exact review for status-first recovery. Never mint a new ID
  /// merely because an earlier Apply timed out or its response was lost.
  factory NativeLogoutReview.fromBytes(List<int> bytes) {
    try {
      if (bytes.isEmpty || bytes.length > 32 << 10) {
        throw const FormatException();
      }
      final value = jsonDecode(utf8.decode(bytes));
      if (value is! Map<String, dynamic> ||
          value.length != 3 ||
          value['version'] != 1 ||
          value['endpoint'] is! String ||
          value['review'] is! String) {
        throw const FormatException();
      }
      final endpoint = _normalizeEndpoint(
        Uri.parse(value['endpoint'] as String),
        allowInsecure: false,
      );
      final wire = $security.CurrentSessionRevocationReview.fromBuffer(
        base64Decode(value['review'] as String),
      );
      _validateNativeReview(wire);
      return NativeLogoutReview._(endpoint, wire..freeze());
    } on Object {
      throw _invalidArgumentException('invalid native logout review');
    }
  }

  /// Bounded opaque recovery bytes. Generated RPC classes remain private.
  Uint8List toBytes() => Uint8List.fromList(
    utf8.encode(
      jsonEncode({
        'version': 1,
        'endpoint': _endpoint.toString(),
        'review': base64Encode(_wire.writeToBuffer()),
      }),
    ),
  );
  @override
  String toString() => 'NativeLogoutReview(redacted)';
}

/// Original durable outcome, independent of local app teardown.
enum NativeOperationOutcome {
  /// Missing/unknown/retired result. This never proves nonexecution or rollback.
  unresolved,

  /// The original operation was applied.
  applied,

  /// The original operation was durably rejected. A new session is not implied.
  rejected,
}

/// Local teardown instruction and independently known durable logout outcome.
final class NativeLogoutResult {
  NativeLogoutResult._(this.clearLocalSession, this.outcome);

  /// Stop old work and remove the local credential even if outcome is unresolved.
  final bool clearLocalSession;

  /// Original durable outcome, when currently disclosable.
  final NativeLogoutOutcome outcome;
}

/// Original logout outcome, independent of local app teardown.
typedef NativeLogoutOutcome = NativeOperationOutcome;

/// Original registration outcome; unknown never authorizes another dispatch.
typedef NativeRegistrationOutcome = NativeOperationOutcome;
