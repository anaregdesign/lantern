/// Base class for content-free offline repository failures.
sealed class OfflineException implements Exception {
  /// Creates a typed offline failure.
  const OfflineException(this.code);

  /// Stable diagnostic-safe category.
  final String code;

  @override
  String toString() => 'OfflineException($code)';
}

/// A caller supplied an invalid offline API argument.
final class OfflineArgumentException extends OfflineException {
  /// Creates an invalid-argument failure.
  const OfflineArgumentException() : super('invalid_argument');
}

/// A mutation is intentionally unavailable in the durable offline contract.
final class OfflineUnsupportedOperationException extends OfflineException {
  /// Creates an unsupported-operation failure.
  const OfflineUnsupportedOperationException() : super('unsupported_operation');
}

/// A cache or outbox record could not be decoded safely.
final class OfflineCodecException extends OfflineException {
  /// Creates a fail-closed codec failure.
  const OfflineCodecException() : super('codec');
}

/// A storage schema cannot be opened or migrated safely.
final class OfflineSchemaException extends OfflineException {
  /// Creates a schema failure.
  const OfflineSchemaException() : super('schema');
}

/// A cache or outbox capacity limit would be exceeded.
final class OfflineCapacityException extends OfflineException {
  /// Creates a bounded-capacity failure.
  const OfflineCapacityException() : super('capacity');
}

/// The durable identity family that collided with retained work.
enum OfflineIdentityKind {
  /// A logical operation ID collided.
  operation,

  /// An individual outbox record ID collided.
  record,
}

/// A durable operation or record identity is already owned by other work.
final class OfflineIdentityConflictException extends OfflineException {
  /// Creates a fail-closed identity-collision failure.
  const OfflineIdentityConflictException(this.kind)
    : super('identity_conflict');

  /// Collision family without exposing the identity itself.
  final OfflineIdentityKind kind;
}

/// A bounded generated-ID allocation attempt could not find a free identity.
final class OfflineIdGenerationException extends OfflineException {
  /// Creates a generated-identity exhaustion failure.
  const OfflineIdGenerationException() : super('id_generation_exhausted');
}

/// A transaction callback tried to use its transaction after it completed.
final class OfflineTransactionClosedException extends OfflineException {
  /// Creates a sealed-transaction failure.
  const OfflineTransactionClosedException() : super('transaction_closed');
}

/// A successful transaction callback produced an inconsistent durable graph.
///
/// Stores must reject the entire commit without publishing state or change
/// notifications when this failure is raised.
final class OfflineDurableGraphException extends OfflineException {
  /// Creates a fail-closed durable-graph failure.
  const OfflineDurableGraphException() : super('durable_graph');
}

/// A caller is not authorized to inspect a sensitive dead-letter intent.
final class OfflineAuthorizationException extends OfflineException {
  /// Creates an authorization failure.
  const OfflineAuthorizationException() : super('authorization');
}

/// An operation was canceled before it could safely continue.
final class OfflineCanceledException extends OfflineException {
  /// Creates a cancellation failure.
  const OfflineCanceledException() : super('canceled');
}

/// Replay is durably paused until credentials rotate and resume is explicit.
final class OfflineAuthPausedException extends OfflineException {
  /// Creates an authentication-pause failure.
  const OfflineAuthPausedException() : super('auth_paused');
}

/// Repository work was requested after process-local disposal.
final class OfflineDisposedException extends OfflineException {
  /// Creates a disposed-repository failure.
  const OfflineDisposedException() : super('disposed');
}

/// A remote response does not satisfy the offline protocol contract.
final class OfflineRemoteProtocolException extends OfflineException {
  /// Creates a remote-protocol failure.
  const OfflineRemoteProtocolException() : super('remote_protocol');
}
