import 'dart:typed_data';
import 'package:lantern_client/lantern_client.dart';
import 'bound_data_client.dart';
import 'errors.dart';
import 'store.dart';
import 'types.dart';

/// Paired local admission/reopen qualification shared by memory and real SQLite.
/// The fixture owns paths and shutdown; this suite never asks for a token or RPC.
Future<void> runOfflineDataClientConformance({
  required Future<OfflineStore> Function() createStore,
  required Future<OfflineStore> Function(OfflineStore) reopenStore,
  required AdmissionDurability durability,
}) async {
  var now = DateTime.utc(2026, 10, 1);
  var tokens = 0;
  final endpoint = Uri.parse('http://127.0.0.1:1');
  final binding = LanternSessionBinding.application(
    serviceOrigin: endpoint,
    securityDomain: 'conformance',
    accountIdentity: 'one',
    authorityBinding: 'a',
  );
  Future<OfflineLanternDataClient> open(
    OfflineStore s, {
    bool cacheReads = true,
  }) => OfflineLanternDataClient.open(
    client: LanternClient.connect(
      endpoint,
      allowInsecure: true,
      clock: () => now,
      tokenProvider: () {
        tokens++;
        throw StateError('unexpected token acquisition');
      },
    ),
    store: s,
    binding: binding,
    durability: durability,
    clock: () => now,
    cacheReads: cacheReads,
  );
  var store = await createStore();
  var client = await open(store);
  final ids = <OperationId>[];
  try {
    final requests = <MutationRequest<Object?>>[
      MutationRequest.putVertices([
        VertexInput(
          key: 'v',
          value: VertexValue.string('pending'),
          expiresIn: const Duration(minutes: 2),
        ),
      ]),
      MutationRequest.putVerticesIfAbsent([
        VertexInput(key: 'conditional', value: VertexValue.nil()),
      ]),
      MutationRequest.putEdges([EdgeInput(tail: 't', head: 'h', weight: .1)]),
      MutationRequest.addEdges([EdgeInput(tail: 't', head: 'h', weight: .1)]),
      MutationRequest.deleteVertices(['v', 'v', 'missing']),
      MutationRequest.deleteEdges([const EdgeRef('t', 'h')]),
      MutationRequest.deleteEdgeContributions([
        EdgeContributionRef(
          tail: 't',
          head: 'h',
          contribId: Uint8List.fromList(List.filled(24, 1)),
        ),
      ]),
    ];
    for (final request in requests) {
      final ticket = await client.mutations.submit(request);
      ids.add(ticket.reference.operationId);
      _require(ticket.durability == durability, 'admission durability');
    }
    _require(tokens == 0, 'zero token acquisition');
    final before = await store.transaction(
      (tx) => tx.outbox(binding.stableIdentity),
    );
    _require(before.length == 9, 'atomic family count');
    _require(
      before.first.intent.expiration == now.add(const Duration(minutes: 2)),
      'absolute TTL',
    );
    _require(
      before[3].intent is OfflineReceiptAddEdgeIntent,
      'fixed Add intent',
    );
    final add =
        (before[3].intent as OfflineReceiptAddEdgeIntent).contributionId;
    _require(add.length == 24 && add.any((b) => b != 0), 'nonzero Add ID');
    final view = await client.getVertex(
      'v',
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    _require(
      view.projection == PendingProjection.pendingDelete &&
          view.confirmed.state == ConfirmedState.unknown,
      'delete is not confirmed Missing',
    );
    await client.close();
    store = await reopenStore(store);
    client = await open(store);
    final after = await store.transaction(
      (tx) => tx.outbox(binding.stableIdentity),
    );
    _require(
      after.first.intent.expiration == before.first.intent.expiration,
      'TTL survived reopen',
    );
    _require(
      (after[3].intent as OfflineReceiptAddEdgeIntent).contributionId.join(
            ',',
          ) ==
          add.join(','),
      'Add identity survived reopen',
    );
    _require(
      after.every((r) => !r.mayHaveDispatched),
      'never-dispatched proof',
    );
    _require(
      after.where((r) => r.preparation is OfflineAwaitingPreparation).length ==
          7,
      'awaiting preparation survived reopen',
    );
    await _fails(
      () => client.mutations.submit(
        MutationRequest.deleteVertices(['v']),
        options: MutationOptions(operationId: ids.first),
      ),
      DataFailure.operationIdCollision,
    );
    now = now.add(const Duration(minutes: 6));
    await client.getVertex(
      'v',
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    now = now.subtract(const Duration(minutes: 6));
    await _fails(
      () => client.mutations.submit(
        MutationRequest.deleteVertices(['v']),
        options: MutationOptions(operationId: ids.first),
      ),
      DataFailure.operationIdExpired,
    );
    try {
      await store.transaction(
        (tx) => tx.updateOutbox(after.first.copyWith(mayHaveDispatched: true)),
      );
      throw StateError('unpaired dispatch evidence accepted');
    } on OfflineDurableGraphException {
      /* Must roll back in both adapters. */
    }
    _require(
      !(await store.transaction(
        (tx) => tx.getOutbox(binding.stableIdentity, after.first.recordId),
      ))!.mayHaveDispatched,
      'dispatch proof rollback',
    );
    // Both adapters reject replacement of the immutable admitted family.
    final op = (await store.transaction(
      (tx) => tx.getOperation(binding.stableIdentity, ids.first.encoded),
    ))!;
    try {
      await store.transaction(
        (tx) => tx.putOperation(
          OfflineOperationRecord(
            partitionId: op.partitionId,
            generation: op.generation,
            operationId: op.operationId,
            items: op.items,
            updatedAt: op.updatedAt,
            kind: MutationKind.addEdges,
            guardUntil: op.guardUntil,
            groupId: op.groupId,
          ),
        ),
      );
      throw StateError('mismatched common kind accepted');
    } on OfflineIdentityConflictException {
      /* Existing immutable topology cannot be replaced. */
    }
    now = client.clock();
    await store.transaction((tx) async {
      // Independently confirmed Edge evidence requires live endpoint evidence.
      for (final key in ['t', 'h']) {
        await tx.putCache(
          binding.stableIdentity,
          OfflineCacheRecord.value(
            partitionId: binding.stableIdentity,
            generation: 0,
            key: OfflineEntityKey.vertex(key),
            entity: Vertex(
              key: key,
              value: VertexValue.nil(),
              expiration: now.add(const Duration(minutes: 1)),
            ),
            validatedAt: now,
            lastAccessAt: now,
          ),
        );
      }
      await tx.putCache(
        binding.stableIdentity,
        OfflineCacheRecord.value(
          partitionId: binding.stableIdentity,
          generation: 0,
          key: OfflineEntityKey.edge('t', 'h'),
          entity: Edge(
            tail: 't',
            head: 'h',
            weight: 5,
            expiration: now.add(const Duration(seconds: 2)),
          ),
          validatedAt: now,
          lastAccessAt: now,
        ),
      );
      await tx.putCache(
        binding.stableIdentity,
        OfflineCacheRecord.missing(
          partitionId: binding.stableIdentity,
          generation: 0,
          key: OfflineEntityKey.vertex('negative'),
          validatedAt: now,
          lastAccessAt: now,
          missingUntil: now.add(const Duration(seconds: 2)),
        ),
      );
    });
    final edge = await client.getEdge(
      const EdgeRef('t', 'h'),
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    _require(
      edge.confirmed.state == ConfirmedState.present &&
          edge.view?.weight == 5 &&
          edge.projection == PendingProjection.unresolvedPending &&
          edge.pending.unresolvedCount == 1,
      'ordered Put Add Delete contribution Delete keeps eligible confirmed base',
    );
    final missing = await client.getVertex(
      'negative',
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    final unknown = await client.getVertex(
      'new',
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    _require(
      missing.confirmed.state == ConfirmedState.missing &&
          unknown.confirmed.state == ConfirmedState.unknown,
      'missing and unknown remain distinct',
    );
    final disabled = await open(store, cacheReads: false);
    try {
      final withoutCache = await disabled.getEdge(
        const EdgeRef('t', 'h'),
        options: const GetOptions(source: ReadSource.cacheOnly),
      );
      _require(
        withoutCache.confirmed.state == ConfirmedState.unknown &&
            withoutCache.view == null,
        'cache-disabled profile cannot use retained base',
      );
    } finally {
      await disabled.close();
    }
    now = now.add(const Duration(seconds: 3));
    final expiredEdge = await client.getEdge(
      const EdgeRef('t', 'h'),
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    final expiredMissing = await client.getVertex(
      'negative',
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    _require(
      expiredEdge.confirmed.state == ConfirmedState.expired &&
          expiredEdge.view == null &&
          expiredMissing.confirmed.state == ConfirmedState.expired,
      'positive and negative TTL are never extended',
    );
    await client.mutations.submit(
      MutationRequest.putEdges([EdgeInput(tail: 't', head: 'h', weight: 2)]),
    );
    final overwrite = await client.getEdge(
      const EdgeRef('t', 'h'),
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    _require(
      overwrite.view?.weight == 2 &&
          overwrite.projection == PendingProjection.exactPendingPut &&
          overwrite.pending.unresolvedCount == 0 &&
          overwrite.pending.wholeSequenceExact,
      'later exact Put resolves prior uncertainty without numeric estimate',
    );
    final count = (await store.transaction(
      (tx) => tx.outbox(binding.stableIdentity),
    )).length;
    try {
      await client.mutations.submitGroup(
        MutationGroup([
          MutationGroupNode(
            MutationRequest.deleteVertices(['cycle']),
            dependencies: [const MutationDependency(node: 1, item: 0)],
          ),
          MutationGroupNode(MutationRequest.deleteVertices(['cycle'])),
        ]),
      );
      throw StateError('cycle accepted');
    } on OfflineDurableGraphException {
      /* Atomic rejection is the contract. */
    }
    _require(
      (await store.transaction(
            (tx) => tx.outbox(binding.stableIdentity),
          )).length ==
          count,
      'cycle rollback',
    );
    _require(tokens == 0, 'reobserve/status/admission stay local');
    for (final id in ids) {
      final status = await client.mutations.getStatus(id);
      _require(
        status.retention == MutationRetention.retained && !status.terminal,
        'retained pending status',
      );
    }
    final resetId = OperationId.generate(issuedAt: now);
    await client.mutations.submit(
      MutationRequest.deleteVertices(['reset']),
      options: MutationOptions(operationId: resetId),
    );
    await client.close();
    store = await reopenStore(store);
    client = await open(store);
    await _fails(
      () => client.mutations.submit(
        MutationRequest.deleteVertices(['reset']),
        options: MutationOptions(operationId: resetId),
      ),
      DataFailure.operationIdCollision,
    );
    await client.close();
    await store.transaction(
      (tx) => tx.bindAuthority(binding.stableIdentity, 'refreshed-authority'),
    );
    client = await open(store);
    await _fails(
      () => client.mutations.submit(
        MutationRequest.deleteVertices(['reset']),
        options: MutationOptions(operationId: resetId),
      ),
      DataFailure.operationIdCollision,
    );
    final old = client;
    final generation = client.generation;
    // Explicit destructive reset: quiesce first; no Server cancellation is claimed.
    await client.close();
    await store.transaction((tx) => tx.wipePartition(binding.stableIdentity));
    client = await open(store);
    _require(
      client.generation == generation + 1 &&
          (await client.mutations.getStatus(resetId)).retention ==
              MutationRetention.unknown &&
          (await store.transaction(
            (tx) => tx.outbox(binding.stableIdentity),
          )).isEmpty,
      'wipe ends old generation and local collision history',
    );
    await _fails(
      () => old.getVertex(
        'reset',
        options: const GetOptions(source: ReadSource.cacheOnly),
      ),
      DataFailure.closed,
    );
    final resubmitted = await client.mutations.submit(
      MutationRequest.deleteVertices(['reset']),
      options: MutationOptions(operationId: resetId),
    );
    _require(
      resubmitted.reference.operationId == resetId,
      'explicit fresh-ID reuse after wipe is a new invocation',
    );
    now = now.add(const Duration(minutes: 6));
    await client.getVertex(
      'reset',
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    await client.close();
    await store.transaction((tx) => tx.wipePartition(binding.stableIdentity));
    now = now.subtract(const Duration(minutes: 6));
    client = await open(store);
    await _fails(
      () => client.mutations.submit(
        MutationRequest.deleteVertices(['reset']),
        options: MutationOptions(operationId: resetId),
      ),
      DataFailure.operationIdExpired,
    );
    _require(
      tokens == 0,
      'reset fixture performs no automatic replay/resubmission',
    );
  } finally {
    await client.close();
  }
}

void _require(bool valid, String label) {
  if (!valid) throw StateError('Offline facade conformance: $label');
}

Future<void> _fails(Future<Object?> Function() call, DataFailure reason) async {
  try {
    await call();
  } on LanternDataException catch (e) {
    _require(e.reason == reason, 'typed ${reason.name}');
    return;
  }
  throw StateError('Offline facade conformance: expected ${reason.name}');
}
