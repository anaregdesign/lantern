/// Base class for content-free offline repository failures.
sealed class OfflineException implements Exception {
  /// Creates a typed offline failure.
  const OfflineException(this.code);

  /// Stable diagnostic-safe category.
  final String code;

  @override
  String toString() => 'OfflineException($code)';
}

/// Startup failed and its provisional ownership could not be safely retired.
/// The reservation stays blocked. Causes are available for private diagnostics;
/// formatting this exception never includes their contents.
final class OfflineStartupRetirementException extends OfflineException {
  /// Preserves the original startup cause and the first failed cleanup cause.
  const OfflineStartupRetirementException(
    this.startupError,
    this.startupTrace,
    this.retirementError,
    this.retirementTrace,
  ) : super('startup_retirement_blocked');

  /// Original startup failure; do not log private exception contents.
  final Object startupError;

  /// Original startup stack, for private diagnostics only.
  final StackTrace startupTrace;

  /// First cleanup failure, for private diagnostics only.
  final Object retirementError;

  /// Cleanup stack, for private diagnostics only.
  final StackTrace retirementTrace;
}

/// Another automatic lifetime owns this backing store.
final class OfflineOwnerConflictException extends OfflineException {
  /// Creates a content-free ownership conflict.
  const OfflineOwnerConflictException() : super('owner_conflict');
}

/// The native owner reservation could not be safely acquired or released.
final class OfflineOwnerStorageException extends OfflineException {
  /// Creates a content-free native storage failure.
  const OfflineOwnerStorageException() : super('owner_storage');
}

/// A renewable network attempt ended; the local account lifetime may continue.
final class OfflineWorkInterruptedException extends OfflineException {
  /// Creates a content-free work-epoch failure, never a retryable wire failure.
  const OfflineWorkInterruptedException() : super('work_interrupted');
}

/// A complete mutation was handled without disclosing its effect.
///
/// This acknowledgement is neither an RPC failure nor an authoritative result.
/// Remote adapters may throw it only for a complete, directly acknowledged call;
/// a partial batch or transport failure must retain its original uncertainty.
final class OfflineMutationAcceptedUndisclosed implements Exception {
  /// Creates a content-free acknowledgement.
  const OfflineMutationAcceptedUndisclosed();

  @override
  String toString() => 'OfflineMutationAcceptedUndisclosed';
}

/// A caller supplied an invalid offline API argument.
final class OfflineArgumentException extends OfflineException {
  /// Creates an invalid-argument failure.
  const OfflineArgumentException() : super('invalid_argument');
}

/// An expired/replaced item claim cannot dispatch or publish a late callback.
final class OfflineClaimLostException extends OfflineException {
  /// Creates a content-free claim-lifetime failure.
  const OfflineClaimLostException() : super('claim_lost');
}

/// A mutation is intentionally unavailable in the durable offline contract.
final class OfflineUnsupportedOperationException extends OfflineException {
  /// Creates an unsupported-operation failure.
  const OfflineUnsupportedOperationException() : super('unsupported_operation');
}

/// Why receipt capability could not authorize a durable enqueue.
enum OfflineReceiptCapabilityFailure {
  /// The endpoint did not advertise active receipt support.
  disabled,

  /// The endpoint did not advertise the requested mutation family.
  mutationUnavailable,
}

/// A fresh receipt-required enqueue could not obtain usable capability.
final class OfflineReceiptCapabilityException extends OfflineException {
  /// Creates a content-free capability failure.
  const OfflineReceiptCapabilityException(this.failure)
    : super('receipt_capability');

  /// Exact capability failure category.
  final OfflineReceiptCapabilityFailure failure;
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

/// CDC chunks were incomplete or interleaved and require checkpoint recovery.
final class OfflineChangeGapException extends OfflineException {
  /// Creates a content-free, fail-closed CDC assembly failure.
  const OfflineChangeGapException() : super('change_gap');
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
