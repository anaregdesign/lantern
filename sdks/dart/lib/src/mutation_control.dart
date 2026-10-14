part of 'client.dart';

/// Immutable request-index-aligned known Server handling.
final class MutationBatchReply<T> {
  /// Owns a fixed list, retaining duplicate identities and undisclosed effects.
  MutationBatchReply(Iterable<MutationReply<T>> items)
    : items = List.unmodifiable(items);

  /// One result per request position.
  final List<MutationReply<T>> items;
}

/// Certainty of one item in an incomplete logical mutation.
enum MutationItemState {
  /// Server handling is known, disclosed or undisclosed.
  handled,

  /// This item failed before possible dispatch.
  failed,

  /// No eligible evidence proves presence or absence.
  unknown,

  /// This item was definitely not sent.
  notAttempted,

  /// Admitted work has no terminal result yet.
  stillPending,
}

/// Request position and dispatch/result certainty.
final class MutationItemStatus<T> {
  /// Creates item evidence; only handled items carry a successful reply.
  const MutationItemStatus({
    required this.index,
    required this.state,
    this.reply,
    this.failure,
  });

  /// Original input index, including duplicate identities.
  final int index;

  /// Dispatch/result certainty.
  final MutationItemState state;

  /// Known handling, including undisclosed acceptance.
  final MutationReply<T>? reply;

  /// Typed local or transport failure.
  final Exception? failure;
}

/// A partial failure retaining every input position.
final class MutationBatchException<T> implements Exception {
  /// Owns immutable per-item progress without interpreting a Delete count.
  MutationBatchException({
    required Iterable<MutationItemStatus<T>> items,
    required this.cause,
    this.reference,
  }) : items = List.unmodifiable(items);

  /// Request-aligned progress.
  final List<MutationItemStatus<T>> items;

  /// Failure which ended this direct attempt or waiter.
  final Exception cause;

  /// Admitted invocation identity, if available.
  final OperationId? reference;
  @override
  String toString() => 'MutationBatchException(${items.length} items)';
}

/// Structured local invocation identity. It is not Server idempotency evidence.
final class OperationId {
  /// Creates a version-one immutable identity from issuance time and entropy.
  OperationId({required DateTime issuedAt, required Uint8List nonce})
    : issuedAt = _normalizeTimestamp(issuedAt),
      _nonce = Uint8List.fromList(nonce) {
    if (nonce.length != 24 || !nonce.any((b) => b != 0)) {
      throw _invalidArgumentException(
        'operation nonce must be 24 nonzero bytes',
      );
    }
  }

  /// Generates fresh platform-secure entropy.
  factory OperationId.generate({DateTime? issuedAt}) {
    final random = Random.secure();
    return OperationId(
      issuedAt: issuedAt ?? DateTime.now().toUtc(),
      nonce: Uint8List.fromList(List.generate(24, (_) => random.nextInt(256))),
    );
  }

  /// Immutable issuance time for bounded reuse admission.
  final DateTime issuedAt;
  final Uint8List _nonce;

  /// Independent nonce bytes.
  Uint8List get nonce => Uint8List.fromList(_nonce);

  /// Versioned storage representation without credentials.
  String get encoded =>
      '1:${issuedAt.microsecondsSinceEpoch}:${base64Url.encode(_nonce)}';
  @override
  bool operator ==(Object other) =>
      other is OperationId && encoded == other.encoded;
  @override
  int get hashCode => encoded.hashCode;
  @override
  String toString() => 'OperationId(redacted)';
}

/// Admission durability, independent of waiter Future lifetime.
enum AdmissionDurability {
  /// No retained evidence or durability.
  none,

  /// Admission is retained in bounded volatile memory.
  memory,

  /// Admission committed to durable storage.
  durable,
}

/// Waiting policy; after admission cancellation detaches observation only.
final class WaitOptions {
  /// Creates waiter options, without altering RPC dispatch or TTL.
  const WaitOptions({this.timeout, this.cancellation});

  /// Maximum waiter duration.
  final Duration? timeout;

  /// Detaches this waiter only.
  final LanternCancellationToken? cancellation;
}

/// Per-invocation local admission and optional waiting policy.
final class MutationOptions {
  /// Creates options; RPC retry/deadline policy belongs to session config.
  const MutationOptions({
    this.admissionCancellation,
    this.operationId,
    this.wait,
  });

  /// May abort validation/reservation before admission, never after it.
  final LanternCancellationToken? admissionCancellation;

  /// Fresh structured invocation ID; retained or aged reuse never sends.
  final OperationId? operationId;

  /// Independent completion observation options.
  final WaitOptions? wait;
}

/// Typed reference to admitted work; reobserve never submits it.
final class MutationReference<T> {
  /// Creates a typed local reference for one bound lifetime/store.
  const MutationReference(this.operationId);

  /// Invocation identity.
  final OperationId operationId;
}

/// Known admission and optional Server-completion observation.
final class MutationTicket<T> {
  /// Creates a ticket without an offline-only handle type.
  const MutationTicket({
    required this.reference,
    required this.admittedAt,
    required this.durability,
    this.completion,
  });

  /// Stable typed status/observation reference.
  final MutationReference<T> reference;

  /// Immutable admission time.
  final DateTime admittedAt;

  /// Durability of admission, never a Server success claim.
  final AdmissionDurability durability;

  /// Optional bounded live observation requested at submit; it does not survive restart.
  final Future<MutationObservation<T>>? completion;
}

/// Result history availability, independent of dispatch certainty.
enum MutationRetention {
  /// The status/result is retained.
  retained,

  /// No eligible evidence proves presence or absence.
  unknown,

  /// The bounded retention window has ended.
  evicted,
}

/// Content-free operation status with typed request-aligned progress.
final class MutationStatus<T> {
  /// Creates a status; unavailable history never proves nonexecution.
  MutationStatus({
    required this.retention,
    required this.operationId,
    Iterable<MutationItemStatus<T>> items = const [],
    this.terminal = false,
    this.observationLost = false,
  }) : items = List.unmodifiable(items);

  /// History certainty.
  final MutationRetention retention;

  /// Original invocation identity.
  final OperationId operationId;

  /// Bounded original item progress.
  final List<MutationItemStatus<T>> items;

  /// Whether all retained items have terminal certainty.
  final bool terminal;

  /// Subscriber overflow dropped intermediate status history.
  final bool observationLost;
}

/// A reobservation can have no retained result; that never dispatches new work.
final class MutationObservation<T> {
  /// Creates retained, unknown or evicted observation.
  const MutationObservation({required this.retention, this.reply});

  /// Retained result availability.
  final MutationRetention retention;

  /// Complete known handling if retained.
  final MutationBatchReply<T>? reply;
}

/// Supported logical mutation families.
enum MutationKind {
  /// Unconditional Vertex Put with exact PutOutcome results.
  putVertices,

  /// Conditional Vertex Put with exact PutOutcome results.
  putVerticesIfAbsent,

  /// Unconditional Edge Put with exact PutOutcome results.
  putEdges,

  /// Additive writes with exact effective binary32 results.
  addEdges,

  /// Vertex Delete with per-index existed results.
  deleteVertices,

  /// Edge Delete with per-index existed results.
  deleteEdges,

  /// Targeted contribution Delete with per-index existed results.
  deleteEdgeContributions,
}

/// Frozen typed logical input for admission, distinct from wire receipts.
sealed class MutationRequest<T> {
  const MutationRequest._(this.kind);

  /// Family with its exact result type.
  final MutationKind kind;

  /// Immutable request positions. The kind fixes their input and effect types.
  /// Qualified admission/storage adapters can persist these without RPC types.
  List<Object> get inputs;

  /// Unconditional Vertex Put request.
  static MutationRequest<PutOutcome> putVertices(
    Iterable<VertexInput> inputs,
  ) => _MutationRequest<PutOutcome>(
    MutationKind.putVertices,
    _boundedInputs(inputs),
  );

  /// Conditional Vertex Put request.
  static MutationRequest<PutOutcome> putVerticesIfAbsent(
    Iterable<VertexInput> inputs,
  ) => _MutationRequest<PutOutcome>(
    MutationKind.putVerticesIfAbsent,
    _boundedInputs(inputs),
  );

  /// Unconditional Edge Put request.
  static MutationRequest<PutOutcome> putEdges(Iterable<EdgeInput> inputs) =>
      _MutationRequest<PutOutcome>(
        MutationKind.putEdges,
        _boundedInputs(inputs),
      );

  /// Additive Edge request; missing contribution IDs are generated at admission.
  static MutationRequest<double> addEdges(Iterable<EdgeInput> inputs) =>
      _MutationRequest<double>(MutationKind.addEdges, _boundedInputs(inputs));

  /// Exact Vertex Delete request.
  static MutationRequest<bool> deleteVertices(Iterable<String> keys) =>
      _MutationRequest<bool>(MutationKind.deleteVertices, _boundedInputs(keys));

  /// Exact Edge Delete request.
  static MutationRequest<bool> deleteEdges(Iterable<EdgeRef> edges) =>
      _MutationRequest<bool>(MutationKind.deleteEdges, _boundedInputs(edges));

  /// Targeted contribution Delete request.
  static MutationRequest<bool> deleteEdgeContributions(
    Iterable<EdgeContributionRef> refs,
  ) => _MutationRequest<bool>(
    MutationKind.deleteEdgeContributions,
    _boundedInputs(refs),
  );
}

final class _MutationRequest<T> extends MutationRequest<T> {
  const _MutationRequest(super.kind, this.inputs) : super._();
  @override
  final List<Object> inputs;
}

/// Explicit bounded prerequisite predicate; no Server transaction is promised.
enum MutationDependencyPredicate {
  /// Server handling is known, disclosed or undisclosed.
  handled,

  /// Requires a disclosed effect.
  knownEffect,

  /// Requires disclosed PutOutcome.appliedAndLive.
  putAppliedAndLive,

  /// Requires a disclosed true Delete result.
  deleteExisted,
}

/// One item dependency within the same newly admitted group.
final class MutationDependency {
  /// References one prerequisite item in this group.
  const MutationDependency({
    required this.node,
    required this.item,
    this.predicate = MutationDependencyPredicate.knownEffect,
  });

  /// Prerequisite node index.
  final int node;

  /// Prerequisite input position.
  final int item;

  /// Required handling evidence.
  final MutationDependencyPredicate predicate;
}

/// One typed request and explicit prerequisites in a bounded group.
final class MutationGroupNode {
  /// Freezes dependencies before any group admission.
  MutationGroupNode(
    this.request, {
    Iterable<MutationDependency> dependencies = const [],
  }) : dependencies = List.unmodifiable(dependencies.take(257));

  /// Typed request, retaining its result type at runtime.
  final MutationRequest<Object?> request;

  /// Explicit item dependencies.
  final List<MutationDependency> dependencies;
}

/// Atomic local group admission, never a Server-wide atomic transaction.
final class MutationGroup {
  /// Freezes at most the bound plus one for fail-closed validation.
  MutationGroup(Iterable<MutationGroupNode> nodes)
    : nodes = List.unmodifiable(nodes.take(65));

  /// At most 64 nodes and 256 arcs are eligible.
  final List<MutationGroupNode> nodes;
}

/// Common advanced admission, status and reobservation port.
abstract interface class LanternMutationControl {
  /// Completes on admission; direct admission is bounded memory only.
  Future<MutationTicket<T>> submit<T>(
    MutationRequest<T> request, {
    MutationOptions? options,
  });

  /// Waits for retained Server handling without sending anything.
  Future<MutationObservation<T>> reobserve<T>(
    MutationReference<T> reference, {
    WaitOptions? options,
  });

  /// Reads retained/unknown/evicted status without acquiring a token.
  Future<MutationStatus<Object?>> getStatus(OperationId operationId);

  /// Watches bounded content-free progress; never performs a resubmission.
  Stream<MutationStatus<Object?>> watchStatus(OperationId operationId);

  /// Atomically admits a validated bounded dependency group.
  Future<List<MutationTicket<Object?>>> submitGroup(MutationGroup group);
}

List<T> _boundedInputs<T>(Iterable<T> input) {
  final values = List<T>.unmodifiable(input.take(LanternCrud.maxBatchSize + 1));
  _validateBatch(values.length, LanternCrud.defaultBatchSize);
  return values;
}

/// Shared bounded fresh-ID policy used by every admission profile.
final class OperationIdPolicy {
  /// Default five-minute admission window with 30 seconds of future skew.
  const OperationIdPolicy({
    this.admissionWindow = const Duration(minutes: 5),
    this.futureSkew = const Duration(seconds: 30),
  });

  /// Time after immutable issuance during which a new invocation is eligible.
  final Duration admissionWindow;

  /// Maximum admitted future issuance skew.
  final Duration futureSkew;
  void _validate() {
    if (admissionWindow <= Duration.zero || futureSkew < Duration.zero) {
      throw _invalidArgumentException('invalid operation ID admission policy');
    }
  }

  /// Deadline through which a retained collision guard must remain pinned.
  DateTime retentionDeadline(OperationId id) {
    _validate();
    return id.issuedAt.add(admissionWindow);
  }

  /// Freshness only; callers must separately check retained identity collision.
  bool isFreshAt(OperationId id, DateTime effectiveNow) =>
      retentionDeadline(id).isAfter(effectiveNow) &&
      !id.issuedAt.isAfter(effectiveNow.add(futureSkew));
}
