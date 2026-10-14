part of 'client.dart';

/// Ephemeral registration authentication, distinct from ordinary login.
final class NativeRegistrationAttempt {
  NativeRegistrationAttempt._(this._attempt, this._recovery);
  final NativeLoginAttempt _attempt;
  final NativeRegistrationReview? _recovery;

  /// App-owned plugin inputs; never persist the attempt or provider credentials.
  NativePluginConfiguration get configuration => _attempt.configuration;

  /// Absolute expiry of the original transaction.
  DateTime get expiresAt => _attempt.expiresAt;

  /// True when this authentication may only inspect an original registration.
  bool get recoveryOnly => _recovery != null;
  @override
  String toString() => 'NativeRegistrationAttempt(redacted)';
}

/// Exact endpoint-bound registration review. Persist before Apply dispatch.
/// Its proposed UID is not proof of installation or authority to access data.
final class NativeRegistrationReview {
  NativeRegistrationReview._(this.endpoint, this._wire);
  final $native.NativeRegistrationReview _wire;

  /// Exact endpoint that prepared this original operation.
  final Uri endpoint;

  /// Server-verified identity of this original, used for recovery partitioning.
  NativePrincipal get principal =>
      NativePrincipal._(_wire.actor.issuer, _wire.actor.subject);

  /// Exact native client profile of the original operation.
  String get clientProfileId => _wire.clientProfileId;

  /// Restores original recovery data, never a new operation or credential.
  factory NativeRegistrationReview.fromBytes(List<int> bytes) {
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
      final wire = $native.NativeRegistrationReview.fromBuffer(
        base64Decode(value['review'] as String),
      );
      _validateRegistrationReview(wire);
      return NativeRegistrationReview._(endpoint, wire..freeze());
    } on Object {
      throw _invalidArgumentException('invalid native registration review');
    }
  }

  /// Opaque bounded bytes. These contain no credential or ephemeral proof.
  Uint8List toBytes() => Uint8List.fromList(
    utf8.encode(
      jsonEncode({
        'version': 1,
        'endpoint': endpoint.toString(),
        'review': base64Encode(_wire.writeToBuffer()),
      }),
    ),
  );
  @override
  String toString() => 'NativeRegistrationReview(redacted)';
}

/// Volatile authorization for one original review. Never persist this object.
final class NativeRegistrationPrepared {
  NativeRegistrationPrepared._(
    this.review,
    this._proof,
    this._affinity,
    this.expiresAt,
    this.recoveryOnly,
    this._configuration,
  );
  final String _proof, _affinity;
  final NativePluginConfiguration _configuration;

  /// Persist this non-secret original before calling Apply.
  final NativeRegistrationReview review;

  /// Absolute proof expiry. Recovery requires new provider authentication.
  final DateTime expiresAt;

  /// Recovery proofs only permit status reads, never registration/session Apply.
  final bool recoveryOnly;
  @override
  String toString() => 'NativeRegistrationPrepared(redacted)';
}

/// Durable registration outcome and a separately issued session, if disclosed.
final class NativeRegistrationResult {
  NativeRegistrationResult._(this.outcome, this.sessionCredential);

  /// Original registration outcome. Unknown never proves nonexecution.
  final NativeRegistrationOutcome outcome;

  /// Newly issued native session; null after response loss or session failure.
  /// Never recover a lost raw credential from status; use ordinary login.
  final NativeSessionCredential? sessionCredential;

  /// Registration succeeded but this response contains no session credential.
  bool get signInRequired =>
      outcome == NativeRegistrationOutcome.applied && sessionCredential == null;
  @override
  String toString() => 'NativeRegistrationResult(${outcome.name}, redacted)';
}
