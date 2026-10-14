import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/lantern_client_adapter.dart';
import 'errors.dart';
import 'mutation_state.dart';
import 'store.dart';
import 'types.dart';

/// Network-free atomic intent admission. No remote capability is consulted here.
final class OfflineMutationAdmission {
  /// Binds one verified lifetime and transactional storage scope.
  OfflineMutationAdmission({
    required this.store,
    required this.client,
    required this.partitionId,
    required this.generation,
    required this.clock,
    required this.fence,
    required this.authorityBinding,
    this.idPolicy = const OperationIdPolicy(),
    this.maxBytes = 32 << 20,
  });

  /// Serialized transactional storage; no network calls occur inside its transactions.
  final OfflineStore store;

  /// Bound parent transport used only for normalization or qualified remote attempts.
  final LanternClient client;

  /// Stable local scope; it is not a wire tenant or an authorization grant.
  final String partitionId;

  /// Captured account/store generation fencing this lifetime.
  final int generation;

  /// Injected wall clock, clamped against persisted observation high-water.
  final OfflineClock clock;

  /// Synchronous owner/account/authority check around every awaited boundary.
  final void Function() fence;

  /// Nonsecret authority generation captured for this account scope.
  final String authorityBinding;

  /// Structured invocation freshness and collision-retention policy.
  final OperationIdPolicy idPolicy;

  /// Aggregate reservation bound applied before normalizing aliased large inputs.
  final int maxBytes;

  /// Freezes once inside the transaction at its persisted effective time.
  Future<({OperationId id, DateTime at})> admit<T>(
    MutationRequest<T> request,
    MutationOptions? options,
  ) async {
    final ids = await admitGroup([
      MutationGroupNode(request),
    ], options: options);
    return ids.single;
  }

  /// Atomically admits at most 64 requests and 256 explicit arcs.
  Future<List<({OperationId id, DateTime at})>> admitGroup(
    List<MutationGroupNode> nodes, {
    MutationOptions? options,
  }) async {
    if (nodes.isEmpty ||
        nodes.length > 64 ||
        nodes.fold(0, (n, v) => n + v.dependencies.length) > 256) {
      throw const OfflineArgumentException();
    }
    return store.transaction((tx) async {
      fence();
      if (options?.admissionCancellation?.isCanceled ?? false) {
        throw const OfflineCanceledException();
      }
      if (await tx.generation(partitionId) != generation) {
        throw const LanternDataException(DataFailure.closed);
      }
      if (await tx.authorityBinding(partitionId) != authorityBinding) {
        throw const LanternDataException(DataFailure.closed);
      }
      final at = await tx.effectiveTime(partitionId, clock().toUtc());
      fence();
      final ids = <OperationId>[];
      final frozen = <MutationRequest<Object?>>[];
      var bytes = 0;
      for (var n = 0; n < nodes.length; n++) {
        late OperationId id;
        var allocated = false;
        for (var attempt = 0; attempt < 8; attempt++) {
          id = options?.operationId ?? OperationId.generate(issuedAt: at);
          if (!idPolicy.isFreshAt(id, at)) {
            throw const LanternDataException(DataFailure.operationIdExpired);
          }
          final collision =
              ids.contains(id) ||
              await tx.getOperation(partitionId, id.encoded) != null;
          if (!collision) {
            allocated = true;
            break;
          }
          if (options?.operationId != null) {
            throw const LanternDataException(DataFailure.operationIdCollision);
          }
        }
        if (!allocated) {
          throw const LanternDataException(DataFailure.operationIdCollision);
        }
        ids.add(id);
        final work = client.freezeIndexedMutation(
          nodes[n].request,
          admittedAt: at,
          maxReservedBytes: maxBytes - bytes,
        );
        bytes += work.reservedBytes;
        frozen.add(work.normalizedRequest);
      }
      final all = <OfflineOutboxRecord>[];
      for (var n = 0; n < nodes.length; n++) {
        final deps = <OfflineDependency>[];
        for (final d in nodes[n].dependencies) {
          if (d.node < 0 ||
              d.node >= nodes.length ||
              d.item < 0 ||
              d.item >= frozen[d.node].inputs.length) {
            throw const OfflineArgumentException();
          }
          deps.add(
            OfflineDependency(
              operationId: ids[d.node].encoded,
              itemIndex: d.item,
              predicate: d.predicate,
            ),
          );
        }
        if (frozen[n].inputs.isEmpty) throw const OfflineArgumentException();
        final records = <OfflineOutboxRecord>[];
        for (var i = 0; i < frozen[n].inputs.length; i++) {
          records.add(
            OfflineOutboxRecord(
              recordId: '${ids[n].encoded}:$i',
              operationId: ids[n].encoded,
              itemIndex: i,
              partitionId: partitionId,
              intent: offlineIntent(frozen[n], i),
              enqueuedAt: at,
              ordinal: 0,
              state: OfflineOutboxState.enqueued,
              attemptCount: 0,
              generation: generation,
              dependencies: deps,
            ),
          );
        }
        final assigned = await tx.enqueueAll(records);
        all.addAll(assigned);
        await tx.putOperation(
          OfflineOperationRecord(
            partitionId: partitionId,
            generation: generation,
            operationId: ids[n].encoded,
            kind: frozen[n].kind,
            guardUntil: idPolicy.retentionDeadline(ids[n]),
            groupId: ids.first.encoded,
            items: [
              for (final r in assigned)
                OfflineWriteStatus(
                  recordId: r.recordId,
                  operationId: r.operationId,
                  itemIndex: r.itemIndex,
                  state: OfflineWriteState.locallyCommitted,
                  attemptCount: 0,
                  mayHaveDispatched: false,
                ),
            ],
            updatedAt: at,
          ),
        );
      }
      validateOfflineDependencies(all, await tx.operations(partitionId));
      fence();
      if (options?.admissionCancellation?.isCanceled ?? false) {
        throw const OfflineCanceledException();
      }
      return [for (final id in ids) (id: id, at: at)];
    });
  }
}

/// Maps normalized public intent; relative TTL and generated IDs are already frozen.
OfflineIntent offlineIntent(MutationRequest<Object?> request, int i) {
  final input = request.inputs[i];
  return switch (request.kind) {
    MutationKind.putVertices => OfflinePutVertexIntent(
      _vertex(input as VertexInput),
    ),
    MutationKind.putVerticesIfAbsent => OfflinePutVertexIfAbsentIntent(
      _vertex(input as VertexInput),
    ),
    MutationKind.putEdges => OfflinePutEdgeIntent(_edge(input as EdgeInput)),
    MutationKind.addEdges => OfflineReceiptAddEdgeIntent(
      _edge(input as EdgeInput),
      input.contribId!,
    ),
    MutationKind.deleteVertices => OfflineDeleteVertexIntent(input as String),
    MutationKind.deleteEdges => OfflineDeleteEdgeIntent(input as EdgeRef),
    MutationKind.deleteEdgeContributions => OfflineDeleteEdgeContributionIntent(
      input as EdgeContributionRef,
    ),
  };
}

Vertex _vertex(VertexInput v) =>
    Vertex(key: v.key, value: v.value, expiration: v.expiresAt);
Edge _edge(EdgeInput e) =>
    Edge(tail: e.tail, head: e.head, weight: e.weight, expiration: e.expiresAt);
