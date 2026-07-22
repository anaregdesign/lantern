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
