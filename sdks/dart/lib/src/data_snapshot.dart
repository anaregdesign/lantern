part of 'client.dart';

/// Source selected for one bounded observation.
enum ReadSource {
  /// Use the bound profile default source.
  automatic,

  /// Use local eligible evidence without token or RPC acquisition.
  cacheOnly,

  /// Observe only a bounded Server response.
  serverOnly,
}

/// Evidence about the entity, independent of pending local effects.
enum ConfirmedState {
  /// Eligible exact value is known.
  present,

  /// Server confirmed absence.
  missing,

  /// Known evidence reached its absolute expiration.
  expired,

  /// No eligible evidence proves presence or absence.
  unknown,
}

/// Provenance of confirmed evidence.
enum ObservationOrigin {
  /// Obtained from a Server response.
  server,

  /// Retained eligible confirmed evidence.
  retainedCache,

  /// No retained evidence or durability.
  none,
}

/// Eligibility of an observation at publication.
enum ObservationFreshness {
  /// Eligible observation from this read.
  freshObservation,

  /// Eligible historical fallback, without current freshness.
  historical,

  /// Known invalidation makes prior evidence ineligible.
  invalidated,

  /// Current evidence is unavailable.
  unavailable,
}

/// Interpretation of the local view.
enum PendingProjection {
  /// No retained evidence or durability.
  none,

  /// The pending Put view is exact.
  exactPendingPut,

  /// Local pending absence, never confirmed Missing.
  pendingDelete,

  /// At least one ordered pending effect cannot be represented exactly.
  unresolvedPending,
}

/// Sanitized cause without transport headers, credentials or raw exceptions.
enum ObservationCause {
  /// Current evidence is unavailable.
  unavailable,

  /// Authentication is absent or no longer valid.
  unauthenticated,

  /// Current authority denies observation.
  permissionDenied,

  /// This profile has no eligible retained read.
  noCache,

  /// Bounded paused delivery overflowed and requires revalidation.
  observationLost,
}

/// Live coverage requested by an observer.
enum WatchCoverage {
  /// Periodic bounded refetch without a completeness claim.
  boundedFreshness,

  /// Requires provably complete dependency coverage.
  strictLive,
}

/// Common typed local failure categories.
enum DataFailure {
  /// The bound client lifetime has ended.
  closed,

  /// The explicitly requested capability is unsupported.
  unsupported,

  /// Admission or observation capacity is exhausted.
  capacity,

  /// The invocation identity was already admitted.
  operationIdCollision,

  /// The identity is outside the fresh admission window.
  operationIdExpired,

  /// The reference result type does not match admitted work.
  typeMismatch,

  /// An explicit prerequisite lacked the requested evidence.
  dependencyFailed,

  /// Only the completion waiter was canceled.
  waitCanceled,

  /// Only the completion waiter reached its timeout.
  waitTimeout,

  /// The immutable source TTL expired before dispatch.
  expiredBeforeDispatch,
}

/// A failure of the local contract, with optional admitted-operation identity.
final class LanternDataException implements Exception {
  /// Creates a content-free typed failure.
  const LanternDataException(
    this.reason, {
    this.reference,
    this.dispatchCertainty = MutationDispatchCertainty.mayHaveDispatched,
  });

  /// Stable failure category.
  final DataFailure reason;

  /// Admitted work is never resubmitted by detaching this waiter.
  final OperationId? reference;

  /// Last known aggregate dispatch certainty when an admitted waiter detached.
  final MutationDispatchCertainty dispatchCertainty;
  @override
  String toString() => 'LanternDataException(${reason.name})';
}

/// Confirmed evidence, distinct from a speculative view.
final class ConfirmedEvidence<T> {
  /// Creates consistent evidence. Unknown/Missing/Expired carry no payload.
  ConfirmedEvidence({
    required this.state,
    this.value,
    required this.origin,
    required this.observedAt,
    this.expiration,
    this.missingUntil,
    required this.freshness,
    this.cause,
  }) {
    if ((state == ConfirmedState.present) != (value != null)) {
      throw _invalidArgumentException(
        'present evidence requires exactly one value',
      );
    }
  }

  /// Evidence state.
  final ConfirmedState state;

  /// Exact eligible value, only for present evidence.
  final T? value;

  /// Evidence source.
  final ObservationOrigin origin;

  /// Original observation time, never a graph version.
  final DateTime observedAt;

  /// Absolute source expiration.
  final DateTime? expiration;

  /// Deadline for retained negative evidence.
  final DateTime? missingUntil;

  /// Current evidence eligibility.
  final ObservationFreshness freshness;

  /// Content-free reason for unavailable evidence.
  final ObservationCause? cause;
}

/// Bounded summary of all ordered pending effects for this observation.
final class PendingMetadata {
  /// Creates a pending summary without operation payloads.
  const PendingMetadata({
    this.count = 0,
    this.unresolvedCount = 0,
    this.wholeSequenceExact = true,
    this.unresolvedKindMask = 0,
  });

  /// Relevant active item count.
  final int count;

  /// Effects which cannot be projected exactly.
  final int unresolvedCount;

  /// Whether every ordered effect is represented in the view.
  final bool wholeSequenceExact;

  /// Bounded bitset of unresolved MutationKind values, not an operation list.
  final int unresolvedKindMask;

  /// Immutable unresolved categories, covering the entire ordered sequence.
  Set<MutationKind> get unresolvedKinds => Set.unmodifiable(
    MutationKind.values.where(
      (kind) => unresolvedKindMask & (1 << kind.index) != 0,
    ),
  );
}

/// An application-facing entity observation.
final class LanternSnapshot<T> {
  /// Creates one observation with independent evidence, view and pending state.
  const LanternSnapshot({
    required this.confirmed,
    this.view,
    this.projection = PendingProjection.none,
    this.pending = const PendingMetadata(),
    this.sequence = 0,
    this.observationLost = false,
    this.observationMode = ObservationMode.oneShot,
  });

  /// Completeness/provenance of the observation coordinator.
  final ObservationMode observationMode;

  /// Confirmed Server/cache evidence.
  final ConfirmedEvidence<T> confirmed;

  /// Eligible current local view. Pending deletion is described by projection.
  final T? view;

  /// Projection certainty.
  final PendingProjection projection;

  /// Whole-sequence pending metadata.
  final PendingMetadata pending;

  /// Monotone local observation sequence.
  final int sequence;

  /// Subscriber delivery overflowed; this observation revalidates the gap.
  final bool observationLost;
}

/// Server traversal result, kept separate from exact CRUD evidence.
final class LanternTraversalSnapshot {
  /// Creates an observation; a known empty graph is present, not Unknown.
  const LanternTraversalSnapshot({
    required this.confirmed,
    this.pending = const PendingMetadata(),
    this.sequence = 0,
    this.coverage = WatchCoverage.boundedFreshness,
    this.observationLost = false,
    this.query,
    this.observationMode = ObservationMode.oneShot,
  });

  /// Exact canonical query, separate from any derived Graph payload.
  final LanternTraversalQuery? query;

  /// One-shot or polling observation; never an implicit strict-live claim.
  final ObservationMode observationMode;

  /// Complete derived graph evidence.
  final ConfirmedEvidence<Graph> confirmed;

  /// Pending exact mutations may invalidate the whole result.
  final PendingMetadata pending;

  /// Monotone local publication sequence.
  final int sequence;

  /// Coverage, without a CDC completeness claim.
  final WatchCoverage coverage;

  /// Delivery overflow requires revalidation.
  final bool observationLost;
}

/// Options for a bounded entity observation.
final class GetOptions {
  /// Creates source and waiting policy.
  const GetOptions({
    this.source = ReadSource.automatic,
    this.timeout,
    this.cancellation,
  });

  /// Explicit source selection.
  final ReadSource source;

  /// Observation timeout, independent of mutation waiting.
  final Duration? timeout;

  /// Cancels only this observation.
  final LanternCancellationToken? cancellation;
}

/// Options for a complete typed traversal observation.
final class TraversalReadOptions {
  /// Creates source and waiting policy.
  const TraversalReadOptions({
    this.source = ReadSource.automatic,
    this.timeout,
    this.cancellation,
  });

  /// Server or retained-result source.
  final ReadSource source;

  /// Bounded observation wait.
  final Duration? timeout;

  /// Cancels this read only.
  final LanternCancellationToken? cancellation;
}

/// Options for shared bounded observation registrations.
final class WatchOptions {
  /// Creates watch options. Poll intervals must be finite and positive.
  const WatchOptions({
    this.pollInterval = const Duration(seconds: 30),
    this.coverage = WatchCoverage.boundedFreshness,
    this.source = ReadSource.automatic,
  });

  /// Maximum interval between fresh reads while subscribed.
  final Duration pollInterval;

  /// Required completeness mode.
  final WatchCoverage coverage;

  /// Source of observations.
  final ReadSource source;
}

/// Current bounded session work, not a claim that Server state is synchronized.
final class LanternSyncStatus {
  /// Creates content-free health metadata.
  const LanternSyncStatus({
    this.activeMutations = 0,
    this.closed = false,
    this.reachability = SyncReachability.unknown,
    this.pause = SyncPause.none,
    this.lifecycle = SyncLifecycle.foreground,
    this.terminalAttentionItems = 0,
    this.observationLost = false,
  });

  /// In-memory attempts which have not settled.
  final int activeMutations;

  /// Whether this lifetime has ended.
  final bool closed;

  /// Last bounded endpoint progress.
  final SyncReachability reachability;

  /// Explicit authority/storage/transfer/restore pause.
  final SyncPause pause;

  /// Owner lifecycle.
  final SyncLifecycle lifecycle;

  /// Terminal failures or uncertainty needing explicit attention.
  final int terminalAttentionItems;

  /// A bounded status subscriber missed intermediate transitions.
  final bool observationLost;
}

/// Endpoint progress, without implying a global synchronization barrier.
enum SyncReachability {
  /// No endpoint result has been obtained yet.
  unknown,

  /// The last attempt could not reach an eligible endpoint.
  offline,

  /// Known work is being handled.
  synchronizing,

  /// No active local attempt; this is not proof of remote equality.
  idle,

  /// An automatic implementation is in its retry delay.
  backoff,

  /// An automatic implementation is restoring observation continuity.
  recovering,
}

/// Explicit reason automatic authority-sensitive work is paused.
enum SyncPause {
  /// No pause is known.
  none,

  /// Verified authentication needs renewal.
  auth,

  /// Current policy denied the operation.
  policy,

  /// Application transfer policy has paused work.
  transfer,

  /// Safe local persistence is blocked.
  storage,

  /// Restored storage needs continuity reconciliation.
  restore,
}

/// Lifecycle of the bound session owner.
enum SyncLifecycle {
  /// Active foreground owner.
  foreground,

  /// Quiesced owner awaiting resume.
  suspended,

  /// Closed lifetime, with no further dispatch.
  closed,
}

/// Canonical typed traversal description shared with the exact wire encoder.
final class LanternTraversalQuery {
  /// Validates every family option and normalizes binary32 once for identity.
  LanternTraversalQuery(
    this.seed, {
    required this.traversal,
    this.weighting = TraversalWeighting.serverDefault,
    this.vertexPrefix = '',
  }) : _bytes = _traversalRequest(
         seed,
         traversal,
         weighting,
         vertexPrefix,
       ).writeToBuffer();

  /// Exact seed.
  final String seed;

  /// Typed family and options, including presence/default sentinels.
  final TraversalOptions traversal;

  /// Shared weight transform.
  final TraversalWeighting weighting;

  /// Candidate prefix; it does not prove dependency completeness.
  final String vertexPrefix;
  final Uint8List _bytes;

  /// Version-one canonical normalized wire identity; account ownership is separate.
  String get canonicalKey => '1:${base64Encode(_bytes)}';

  /// Defensive canonical bytes for qualified result-cache codecs.
  Uint8List get canonicalBytes => Uint8List.fromList(_bytes);
}

/// How the current observation was produced, without a completeness guarantee.
enum ObservationMode {
  /// A bounded explicit read.
  oneShot,

  /// An active bounded-freshness polling query.
  pollingObservation,

  /// A local-only observation.
  localObservation,
}
