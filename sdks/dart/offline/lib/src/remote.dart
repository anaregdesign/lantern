import 'package:lantern_client/lantern_client.dart';

import 'change_store.dart';
import 'errors.dart';
import 'types.dart';

/// Typed classification of one remote attempt for replay policy.
enum OfflineRemoteErrorKind {
  /// A temporary transport or endpoint failure.
  unavailable,

  /// The attempt crossed its caller or client deadline.
  deadlineExceeded,

  /// Credentials must be refreshed before another attempt.
  unauthenticated,

  /// The mutation is invalid and must not be retried.
  invalidArgument,

  /// The caller canceled before a receipt mutation could have executed.
  ///
  /// A custom receipt adapter must report cancellation after dispatch as
  /// [outcomeUnknown], not [canceled].
  canceled,

  /// A server resource limit rejected this attempt.
  resourceExhausted,

  /// A permanent server-side failure rejected the intent.
  permanent,

  /// A receipt-bearing mutation may have executed without a response.
  outcomeUnknown,

  /// An unmapped failure that remains retryable within policy bounds.
  unknown,
}

/// A typed remote failure that retains, but never serializes, its cause.
final class OfflineRemoteFailure implements Exception {
  /// Creates a classified remote failure.
  const OfflineRemoteFailure(this.kind, this.cause);

  /// Bounded replay-policy classification.
  final OfflineRemoteErrorKind kind;

  /// Original in-memory typed transport cause.
  final Object cause;
}

/// Exact remote read result.
sealed class OfflineRemoteRead<T> {
  /// Creates a remote read result.
  const OfflineRemoteRead();
}

/// A present exact remote entity.
final class OfflineRemotePresent<T> extends OfflineRemoteRead<T> {
  /// Creates a present remote read.
  const OfflineRemotePresent(this.value);

  /// Exact remote entity.
  final T value;
}

/// A confirmed remote absence.
final class OfflineRemoteMissing<T> extends OfflineRemoteRead<T> {
  /// Creates a confirmed remote absence.
  const OfflineRemoteMissing();
}

/// Network port used by [OfflineLanternRepository].
///
/// Implementations must acquire credentials at send time and honor cancellation.
/// They must return exact values and expirations.
abstract interface class OfflineRemote {
  /// Probes the real Lantern health surface without implying mutation delivery.
  Future<void> probe({LanternCancellationToken? cancellation});

  /// Gets one exact vertex or a confirmed absence.
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  });

  /// Gets one exact edge or a confirmed absence.
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  });

  /// Writes one already expiration-resolved vertex and returns the
  /// server-authoritative application outcome.
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  });

  /// Writes one already expiration-resolved edge and returns the
  /// server-authoritative application outcome.
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  });
}

/// Receipt contexts prepared from one authenticated endpoint capability.
final class OfflineReceiptPreparation {
  /// Creates an immutable preparation aligned to offline operation items.
  OfflineReceiptPreparation({
    required this.mutation,
    required Iterable<OfflineReceiptEvidence> evidence,
  }) : evidence = List<OfflineReceiptEvidence>.unmodifiable(
         evidence.map(copyOfflineReceiptEvidence),
       ) {
    if (this.evidence.isEmpty ||
        this.evidence.any((item) => item.mutation != mutation)) {
      throw const OfflineArgumentException();
    }
    final first = this.evidence.first;
    final operationIds = <ReceiptOperationId>{};
    final groupIds = <ReceiptGroupId>{};
    for (final item in this.evidence) {
      if (!operationIds.add(item.operationId) ||
          !groupIds.add(item.groupId) ||
          item.state != OfflineReceiptReconciliationState.statusRequired ||
          item.reconciliationAttemptCount != 0 ||
          item.endpoint != first.endpoint ||
          item.policy.deploymentEpoch != first.policy.deploymentEpoch ||
          item.policy.retention != first.policy.retention ||
          item.policy.maxEntries != first.policy.maxEntries ||
          item.policy.maxBytes != first.policy.maxBytes ||
          !_sameBytes(item.policy.fingerprint, first.policy.fingerprint)) {
        throw const OfflineArgumentException();
      }
    }
  }

  /// Prepared receipt mutation family.
  final ReceiptMutationKind mutation;

  /// One independent, one-item server receipt group per offline item.
  final List<OfflineReceiptEvidence> evidence;
}

/// Storage-neutral receipt capability observed during reconciliation.
sealed class OfflineReceiptCapability {
  const OfflineReceiptCapability();

  /// Whether bounded receipt reconciliation is enabled.
  bool get enabled;
}

/// Receipt reconciliation is disabled or temporarily uncertified.
final class OfflineReceiptCapabilityDisabled extends OfflineReceiptCapability {
  /// Creates a disabled capability observation.
  const OfflineReceiptCapabilityDisabled();

  @override
  bool get enabled => false;
}

/// Enabled receipt capability with exact endpoint and policy continuity.
final class OfflineReceiptCapabilityEnabled extends OfflineReceiptCapability {
  /// Creates one immutable enabled capability observation.
  OfflineReceiptCapabilityEnabled({
    required this.endpoint,
    required this.policy,
    required Set<ReceiptMutationKind> supportedMutations,
  }) : supportedMutations = Set<ReceiptMutationKind>.unmodifiable(
         supportedMutations,
       );

  /// Exact endpoint continuity marker.
  final ReceiptEndpoint endpoint;

  /// Exact active receipt policy.
  final OfflineReceiptPolicy policy;

  /// Receipt mutation families enabled at the endpoint.
  final Set<ReceiptMutationKind> supportedMutations;

  /// Whether [mutation] is enabled at the endpoint.
  bool supports(ReceiptMutationKind mutation) =>
      supportedMutations.contains(mutation);

  @override
  bool get enabled => true;
}

/// Storage-neutral read-only receipt status.
final class OfflineReceiptStatus {
  /// Creates one exact status observation.
  OfflineReceiptStatus({
    required this.operationId,
    required this.state,
    this.groupId,
    this.mutation,
    this.itemIndex,
    this.itemCount,
    this.result,
  }) {
    final confirmed = state == ReceiptStatusState.confirmed;
    final hasCompleteReceipt =
        groupId != null &&
        mutation != null &&
        itemIndex != null &&
        itemCount != null &&
        result != null;
    final hasAnyReceipt =
        groupId != null ||
        mutation != null ||
        itemIndex != null ||
        itemCount != null ||
        result != null;
    if (!confirmed) {
      if (hasAnyReceipt) throw const OfflineArgumentException();
    } else {
      if (!hasCompleteReceipt) throw const OfflineArgumentException();
      final resultMatchesMutation = switch ((mutation!, result!)) {
        (ReceiptMutationKind.vertexPut, OfflineVertexPutReceiptResult()) =>
          true,
        (
          ReceiptMutationKind.vertexDelete,
          OfflineVertexDeleteReceiptResult(),
        ) =>
          true,
        (ReceiptMutationKind.edgeDelete, OfflineEdgeDeleteReceiptResult()) =>
          true,
        (ReceiptMutationKind.edgeAdd, OfflineEdgeAddReceiptResult()) => true,
        _ => false,
      };
      if (itemIndex! < 0 ||
          itemIndex! >= LanternCrud.maxBatchSize ||
          itemCount! <= 0 ||
          itemCount! > LanternCrud.maxBatchSize ||
          itemIndex! >= itemCount! ||
          !resultMatchesMutation) {
        throw const OfflineArgumentException();
      }
    }
  }

  /// Requested operation ID.
  final ReceiptOperationId operationId;

  /// Current receipt evidence state.
  final ReceiptStatusState state;

  /// Original logical-call group when confirmed.
  final ReceiptGroupId? groupId;

  /// Original mutation family when confirmed.
  final ReceiptMutationKind? mutation;

  /// Original zero-based item index when confirmed.
  final int? itemIndex;

  /// Original item count when confirmed.
  final int? itemCount;

  /// Exact original result when confirmed.
  final OfflineReceiptResult? result;
}

/// Optional receipt-bearing mutation port for durable reconciliation.
///
/// Implementations acquire credentials at call time. Mutation methods issue at
/// most one RPC attempt; the repository owns every status-first resend.
abstract interface class OfflineReceiptRemote {
  /// Learns capability and mints exact contexts before durable enqueue.
  Future<OfflineReceiptPreparation> prepareReceipts(
    ReceiptMutationKind mutation, {
    required int itemCount,
    LanternCancellationToken? cancellation,
  });

  /// Reads one operation's current receipt evidence without mutation.
  Future<OfflineReceiptStatus> getReceiptStatus(
    ReceiptOperationId operationId, {
    LanternCancellationToken? cancellation,
  });

  /// Sends one supported receipt intent with its exact persisted context.
  ///
  /// Any failure after dispatch that cannot prove non-execution must be
  /// classified as [OfflineRemoteErrorKind.outcomeUnknown].
  Future<OfflineReceiptResult> sendReceiptMutation(
    OfflineIntent intent, {
    required ReceiptContext context,
    LanternCancellationToken? cancellation,
  });

  /// Reads current capability for same-endpoint policy proof before resend.
  Future<OfflineReceiptCapability> getReceiptCapability({
    LanternCancellationToken? cancellation,
  });
}

/// Optional bounded plural-read port for resident-only CDC recovery.
///
/// Results must align exactly with request indexes. Callers cap each family at
/// [offlineMaxResidentRevalidationBatch] and reject incomplete responses.
abstract interface class OfflineRecoveryRemote {
  /// Revalidates exact resident Vertex keys in one bounded plural call.
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  });

  /// Revalidates exact resident Edge identities in one bounded plural call.
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  });
}

/// [OfflineRemote] adapter over the official [LanternClient].
///
/// The wrapped client invokes its configured [TokenProvider] at send time. The
/// adapter disables the client's nested retry policy. Each singular adapter
/// method issues at most one RPC attempt; [OfflineLanternRepository] owns
/// durable retry accounting.
/// The adapter neither reads nor persists credentials.
final class LanternClientOfflineRemote
    implements OfflineRemote, OfflineReceiptRemote, OfflineRecoveryRemote {
  /// Wraps one online Lantern client.
  const LanternClientOfflineRemote(this.client);

  /// Underlying online client.
  final LanternClient client;

  @override
  Future<OfflineReceiptPreparation> prepareReceipts(
    ReceiptMutationKind mutation, {
    required int itemCount,
    LanternCancellationToken? cancellation,
  }) async {
    if (itemCount <= 0 || itemCount > LanternCrud.maxBatchSize) {
      throw const OfflineArgumentException();
    }
    late final ReceiptCapability capability;
    try {
      capability = await client.getReceiptCapability(
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
    if (capability is ReceiptCapabilityDisabled) {
      throw const OfflineReceiptCapabilityException(
        OfflineReceiptCapabilityFailure.disabled,
      );
    }
    final enabled = capability as ReceiptCapabilityEnabled;
    if (!enabled.supports(mutation)) {
      throw const OfflineReceiptCapabilityException(
        OfflineReceiptCapabilityFailure.mutationUnavailable,
      );
    }
    try {
      final policy = OfflineReceiptPolicy.fromCapability(enabled);
      return OfflineReceiptPreparation(
        mutation: mutation,
        evidence: List<OfflineReceiptEvidence>.generate(itemCount, (_) {
          final context = client.mintReceiptContext(
            capability: enabled,
            mutation: mutation,
            itemCount: 1,
          );
          return OfflineReceiptEvidence(
            operationId: context.operationIds.single,
            groupId: context.groupId,
            endpoint: context.endpoint,
            mutation: mutation,
            policy: policy,
            itemIndex: 0,
            itemCount: 1,
            state: OfflineReceiptReconciliationState.statusRequired,
          );
        }, growable: false),
      );
    } on OfflineException {
      rethrow;
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<OfflineReceiptCapability> getReceiptCapability({
    LanternCancellationToken? cancellation,
  }) async {
    try {
      final capability = await client.getReceiptCapability(
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
      return switch (capability) {
        ReceiptCapabilityDisabled() => const OfflineReceiptCapabilityDisabled(),
        ReceiptCapabilityEnabled() => OfflineReceiptCapabilityEnabled(
          endpoint: ReceiptEndpoint(
            nodeId: capability.endpoint.nodeId,
            generation: capability.endpoint.generation,
          ),
          policy: OfflineReceiptPolicy.fromCapability(capability),
          supportedMutations: capability.supportedMutations,
        ),
      };
    } on OfflineException {
      rethrow;
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<OfflineReceiptStatus> getReceiptStatus(
    ReceiptOperationId operationId, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      final status = await client.getReceiptStatus(
        operationId,
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
      final receipt = status.receipt;
      final result = switch (receipt) {
        VertexPutReceipt(:final outcome) => OfflineVertexPutReceiptResult(
          outcome,
        ),
        VertexDeleteReceipt(:final existed) => OfflineVertexDeleteReceiptResult(
          existed,
        ),
        EdgeDeleteReceipt(:final existed) => OfflineEdgeDeleteReceiptResult(
          existed,
        ),
        EdgeAddReceipt(:final effectiveWeight) =>
          OfflineEdgeAddReceiptResult(effectiveWeight),
        null => null,
      };
      return OfflineReceiptStatus(
        operationId: status.operationId,
        state: status.state,
        groupId: receipt?.groupId,
        mutation: receipt?.mutation,
        itemIndex: receipt?.itemIndex,
        itemCount: receipt?.itemCount,
        result: result,
      );
    } on OfflineException {
      rethrow;
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<OfflineReceiptResult> sendReceiptMutation(
    OfflineIntent intent, {
    required ReceiptContext context,
    LanternCancellationToken? cancellation,
  }) async {
    try {
      final options = LanternCallOptions(
        cancellation: cancellation,
        retry: false,
      );
      return switch (intent) {
        OfflinePutVertexIfAbsentIntent(:final vertex) =>
          OfflineVertexPutReceiptResult(
            (await client.putVertexWithReceipt(
              VertexInput(
                key: vertex.key,
                value: vertex.value,
                expiresAt: vertex.expiration,
              ),
              context: context,
              ifAbsent: true,
              options: options,
            )).outcome,
          ),
        OfflineDeleteVertexIntent(:final vertexKey) =>
          OfflineVertexDeleteReceiptResult(
            (await client.deleteVertexWithReceipt(
              vertexKey,
              context: context,
              options: options,
            )).existed,
          ),
        OfflineDeleteEdgeIntent(:final edge) => OfflineEdgeDeleteReceiptResult(
          (await client.deleteEdgeWithReceipt(
            edge,
            context: context,
            options: options,
          )).existed,
        ),
        OfflineReceiptAddEdgeIntent(:final edge, :final contributionId) =>
          OfflineEdgeAddReceiptResult(
            (await client.addEdgeWithReceipt(
              EdgeInput(
                tail: edge.tail,
                head: edge.head,
                weight: edge.weight,
                expiresAt: edge.expiration,
                contribId: contributionId,
              ),
              context: context,
              options: options,
            )).effectiveWeight,
          ),
        _ => throw const OfflineUnsupportedOperationException(),
      };
    } on OfflineException {
      rethrow;
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) async {
    try {
      await client.ping(
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      return OfflineRemotePresent<Edge>(
        await client.getEdge(
          edge,
          options: LanternCallOptions(cancellation: cancellation, retry: false),
        ),
      );
    } on LanternNotFoundException {
      return const OfflineRemoteMissing<Edge>();
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  }) async {
    if (edges.isEmpty || edges.length > offlineMaxResidentRevalidationBatch) {
      throw const OfflineArgumentException();
    }
    try {
      final result = await client.getEdges(
        edges,
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
      final present = {
        for (final edge in result.edges)
          OfflineEntityKey.edge(edge.tail, edge.head).canonical: edge,
      };
      final missing = {
        for (final edge in result.missing)
          OfflineEntityKey.edge(edge.tail, edge.head).canonical,
      };
      if (present.length + missing.length != edges.length) {
        throw const OfflineCodecException();
      }
      return List<OfflineRemoteRead<Edge>>.unmodifiable(
        edges.map((edge) {
          final key = OfflineEntityKey.edge(edge.tail, edge.head).canonical;
          final value = present[key];
          if (value != null) return OfflineRemotePresent<Edge>(value);
          if (missing.contains(key)) return const OfflineRemoteMissing<Edge>();
          throw const OfflineCodecException();
        }),
      );
    } on OfflineException {
      rethrow;
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      return OfflineRemotePresent<Vertex>(
        await client.getVertex(
          key,
          options: LanternCallOptions(cancellation: cancellation, retry: false),
        ),
      );
    } on LanternNotFoundException {
      return const OfflineRemoteMissing<Vertex>();
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) async {
    if (keys.isEmpty || keys.length > offlineMaxResidentRevalidationBatch) {
      throw const OfflineArgumentException();
    }
    try {
      final result = await client.getVertices(
        keys,
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
      final present = {
        for (final vertex in result.vertices) vertex.key: vertex,
      };
      final missing = result.missing.toSet();
      if (present.length + missing.length != keys.length) {
        throw const OfflineCodecException();
      }
      return List<OfflineRemoteRead<Vertex>>.unmodifiable(
        keys.map((key) {
          final value = present[key];
          if (value != null) return OfflineRemotePresent<Vertex>(value);
          if (missing.contains(key)) {
            return const OfflineRemoteMissing<Vertex>();
          }
          throw const OfflineCodecException();
        }),
      );
    } on OfflineException {
      rethrow;
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      return await client.putEdge(
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: edge.weight,
          expiresAt: edge.expiration,
        ),
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      return await client.putVertex(
        VertexInput(
          key: vertex.key,
          value: vertex.value,
          expiresAt: vertex.expiration,
        ),
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }
}

/// Maps one official online-client failure into the offline replay taxonomy.
///
/// Custom [OfflineRemote] adapters can use this function to preserve the same
/// retry and terminal-failure policy as [LanternClientOfflineRemote]. A bounded
/// online retry wrapper is classified by its final typed cause while the full
/// wrapper remains available as [OfflineRemoteFailure.cause]. Strict local SDK
/// response validation maps to [OfflineRemoteProtocolException], while a
/// genuine server or transport `INTERNAL` remains retryable uncertainty.
Exception mapLanternClientFailure(Object error) {
  if (error is OfflineRemoteFailure) return error;
  if (error is OfflineCanceledException) return error;
  final classified = error is LanternRetryExhaustedException
      ? error.cause
      : error;
  if (classified is LanternInternalException &&
      classified.isSdkProtocolViolation) {
    return const OfflineRemoteProtocolException();
  }
  if (classified is LanternCanceledException) {
    return const OfflineCanceledException();
  }
  if (classified is ReceiptReconciliationException) {
    return OfflineRemoteFailure(OfflineRemoteErrorKind.outcomeUnknown, error);
  }
  return OfflineRemoteFailure(switch (classified) {
    LanternUnavailableException() => OfflineRemoteErrorKind.unavailable,
    LanternDeadlineExceededException() =>
      OfflineRemoteErrorKind.deadlineExceeded,
    LanternUnauthenticatedException() => OfflineRemoteErrorKind.unauthenticated,
    LanternInvalidArgumentException() => OfflineRemoteErrorKind.invalidArgument,
    LanternResourceExhaustedException() =>
      OfflineRemoteErrorKind.resourceExhausted,
    LanternPermissionDeniedException() ||
    LanternFailedPreconditionException() => OfflineRemoteErrorKind.permanent,
    _ => OfflineRemoteErrorKind.unknown,
  }, error);
}

bool _sameBytes(List<int> left, List<int> right) {
  if (left.length != right.length) return false;
  var difference = 0;
  for (var index = 0; index < left.length; index++) {
    difference |= left[index] ^ right[index];
  }
  return difference == 0;
}
