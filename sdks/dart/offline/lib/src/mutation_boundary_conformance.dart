import 'dart:async';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

import 'bound_data_client.dart';
import 'bound_mutation_remote.dart';
import 'claim_fence.dart';
import 'errors.dart';
import 'mutation_state.dart';
import 'store.dart';
import 'types.dart';

/// Finite claim/publication/TTL counterexamples run unchanged on both adapters.
/// Fake mutation handling isolates these local boundaries; no RPC is performed.
Future<void> runOfflineMutationBoundaryConformance({
  required Future<OfflineStore> Function() createStore,
  required AdmissionDurability durability,
}) async {
  for (final boundary in [
    'wall-expiry',
    'quiet-high-water',
    'reclaim-before',
    'reclaim-after',
  ]) {
    final f = await _BoundaryFixture.open(await createStore(), durability);
    try {
      final ticket = await f.client.mutations.submit(
        MutationRequest.putVertices([_vertex('v')]),
        options: const MutationOptions(wait: WaitOptions()),
      );
      var liveSuccess = false;
      unawaited(
        ticket.completion!.then<void>(
          (_) => liveSuccess = true,
          onError: (Object _, StackTrace _) {},
        ),
      );
      Future<void> change() async {
        if (boundary == 'wall-expiry') {
          f.now = f.now.add(const Duration(minutes: 3));
          return;
        }
        await f.store.transaction((tx) async {
          final original = (await tx.outbox(f.client.partitionId)).single;
          final at = await tx.effectiveTime(
            f.client.partitionId,
            original.leaseUntil!.add(const Duration(seconds: 1)),
          );
          if (boundary.startsWith('reclaim')) {
            final replacement = (await tx.claim(
              f.client.partitionId,
              owner: 'replacement-owner',
              now: at,
              maxAge: const Duration(days: 7),
              leaseDuration: const Duration(minutes: 2),
              limit: 1,
            )).single;
            await advanceOfflineMutation(
              tx,
              replacement,
              OfflineWriteState.sending,
              at,
            );
          }
        });
      }

      if (boundary == 'reclaim-after') {
        f.sender.after = change;
      } else {
        f.sender.before = change;
      }
      _require(
        await f.client.mutationDelivery.dispatchPass() == 0,
        '$boundary no handling',
      );
      _require(
        f.sender.calls == (boundary == 'reclaim-after' ? 1 : 0),
        '$boundary final send fence',
      );
      final status = await f.client.mutations.getStatus(
        ticket.reference.operationId,
      );
      _require(
        status.items.single.reply == null && !liveSuccess,
        '$boundary no stale live success',
      );
      final row = (await f.store.transaction(
        (tx) => tx.outbox(f.client.partitionId),
      )).single;
      _require(
        row.mayHaveDispatched && row.attemptCount == 1,
        '$boundary conservative evidence retained',
      );
      if (boundary.startsWith('reclaim')) {
        _require(
          row.leaseOwner == 'replacement-owner',
          '$boundary replacement survives',
        );
      }
    } finally {
      await f.client.close();
    }
  }

  final rolledBack = await _BoundaryFixture.open(
    await createStore(),
    durability,
  );
  OfflineClaimFence? abortedFence;
  try {
    await rolledBack.client.mutations.submit(
      MutationRequest.putVertices([_vertex('v')]),
    );
    try {
      await rolledBack.store.transaction((tx) async {
        final row = (await tx.claim(
          rolledBack.client.partitionId,
          owner: 'aborted-owner',
          now: rolledBack.now,
          maxAge: const Duration(days: 7),
          leaseDuration: const Duration(minutes: 2),
          limit: 1,
        )).single;
        await advanceOfflineMutation(
          tx,
          row,
          OfflineWriteState.sending,
          rolledBack.now,
        );
        abortedFence = await (tx as OfflineStoreClaimTransaction)
            .captureClaimFence(row, authorityBinding: 'a');
        throw StateError('abort captured claim');
      });
    } catch (_) {}
    var lost = false;
    try {
      abortedFence!.checkCurrent(rolledBack.now);
    } on OfflineClaimLostException {
      lost = true;
    }
    _require(
      lost &&
          (await rolledBack.store.transaction(
                (tx) => tx.outbox(rolledBack.client.partitionId),
              )).single.state ==
              OfflineOutboxState.enqueued,
      'aborted transaction cannot activate a claim fence',
    );
  } finally {
    abortedFence?.release();
    await rolledBack.client.close();
  }

  for (final edge in [false, true]) {
    final f = await _BoundaryFixture.open(await createStore(), durability);
    final entered = Completer<void>(), release = Completer<void>();
    f.sender.after = () async {
      entered.complete();
      await release.future;
    };
    try {
      final request = edge
          ? MutationRequest.putEdges([
              EdgeInput(
                tail: 't',
                head: 'h',
                weight: 1,
                expiresIn: const Duration(seconds: 1),
              ),
            ])
          : MutationRequest.putVertices([
              _vertex('v', ttl: const Duration(seconds: 1)),
            ]);
      final ticket = await f.client.mutations.submit(request);
      final pass = f.client.mutationDelivery.dispatchPass();
      await entered.future;
      f.now = f.now.add(const Duration(seconds: 2));
      Future<LanternSnapshot<Object?>> view() async => edge
          ? f.client.getEdge(
              const EdgeRef('t', 'h'),
              options: const GetOptions(source: ReadSource.cacheOnly),
            )
          : f.client.getVertex(
              'v',
              options: const GetOptions(source: ReadSource.cacheOnly),
            );
      final expired = await view();
      _require(
        expired.view == null &&
            expired.confirmed.state == ConfirmedState.unknown &&
            expired.projection == PendingProjection.unresolvedPending &&
            expired.pending.count == 1,
        'possibly-sent expired ${edge ? 'Edge' : 'Vertex'} is not a live value or Missing',
      );
      final key = edge
          ? OfflineEntityKey.edge('t', 'h')
          : OfflineEntityKey.vertex('v');
      final Object base = edge
          ? Edge(
              tail: 't',
              head: 'h',
              weight: 4,
              expiration: f.now.add(const Duration(minutes: 1)),
            )
          : Vertex(
              key: 'v',
              value: VertexValue.string('independent-base'),
              expiration: f.now.add(const Duration(minutes: 1)),
            );
      await f.store.transaction((tx) async {
        if (edge) {
          for (final endpoint in ['t', 'h']) {
            await tx.putCache(
              f.client.partitionId,
              OfflineCacheRecord.value(
                partitionId: f.client.partitionId,
                generation: f.client.generation,
                key: OfflineEntityKey.vertex(endpoint),
                entity: Vertex(
                  key: endpoint,
                  value: VertexValue.nil(),
                  expiration: f.now.add(const Duration(minutes: 1)),
                ),
                validatedAt: f.now,
                lastAccessAt: f.now,
              ),
            );
          }
        }
        await tx.putCache(
          f.client.partitionId,
          OfflineCacheRecord.value(
            partitionId: f.client.partitionId,
            generation: f.client.generation,
            key: key,
            entity: base,
            validatedAt: f.now,
            lastAccessAt: f.now,
          ),
        );
      });
      final based = await view();
      _require(
        based.confirmed.state == ConfirmedState.present &&
            based.view != null &&
            based.projection == PendingProjection.unresolvedPending,
        'independently eligible base retained',
      );
      _require(
        edge
            ? (based.view as Edge).weight == 4
            : ((based.view as Vertex).value as StringValue).value ==
                  'independent-base',
        'unresolved view uses only the independently confirmed base',
      );
      if (edge) {
        await f.client.mutations.submit(
          MutationRequest.putEdges([
            EdgeInput(
              tail: 't',
              head: 'h',
              weight: 2,
              expiresIn: const Duration(minutes: 1),
            ),
          ]),
        );
      } else {
        await f.client.mutations.submit(MutationRequest.deleteVertices(['v']));
      }
      final later = await view();
      _require(
        later.projection ==
                (edge
                    ? PendingProjection.exactPendingPut
                    : PendingProjection.pendingDelete) &&
            later.pending.count == 2 &&
            later.pending.unresolvedCount == 0,
        'later live Put/Delete overrides expired uncertainty',
      );
      _require(
        edge
            ? (later.view as Edge).weight == 2 &&
                  (later.view as Edge).expiration!.isAfter(f.now)
            : later.view == null,
        'later pending view wins without expired-value revival',
      );
      release.complete();
      _require(await pass == 1, 'eligible late handling');
      final original = await f.client.mutations.reobserve(ticket.reference);
      _require(
        (original.reply!.items.single as MutationKnownEffect<Object?>).effect ==
            PutOutcome.appliedAndLive,
        'original Server handling is not reinterpreted after entity TTL',
      );
      final settled = await view();
      _require(
        settled.pending.count == 1 &&
            settled.projection == later.projection &&
            (edge ? (settled.view as Edge).weight == 2 : settled.view == null),
        'only settled overlay retired; later pending view survives',
      );
    } finally {
      if (!release.isCompleted) release.complete();
      await f.client.close();
    }
  }

  for (final failReads in [false, true]) {
    final raw = await createStore();
    final broken = _BoundaryFaultStore(raw);
    final f = await _BoundaryFixture.open(broken, durability);
    try {
      final ticket = await f.client.mutations.submit(
        MutationRequest.putVertices([_vertex('v')]),
        options: const MutationOptions(wait: WaitOptions()),
      );
      f.sender.after = () async {
        broken.failConfirmation = true;
        broken.failReads = failReads;
      };
      Object? failure;
      try {
        await f.client.mutationDelivery.dispatchPass();
      } catch (error) {
        failure = error;
      }
      _require(
        failure != null && f.client.mutationDelivery.storageBlocked,
        'confirmation fault holds delivery',
      );
      final handling = await ticket.completion!;
      _require(
        (handling.reply!.items.single as MutationKnownEffect<PutOutcome>)
                .effect ==
            PutOutcome.appliedAndLive,
        'current claim live handling survives ${failReads ? 'all-read' : 'confirmation'} fault',
      );
      final row = (await raw.transaction(
        (tx) => tx.outbox(f.client.partitionId),
      )).single;
      _require(
        row.state == OfflineOutboxState.sending && row.mayHaveDispatched,
        'fault retains pre-send evidence',
      );
    } finally {
      await f.client.close();
    }
  }
}

/// Old unbound evidence cannot inherit authority; current qualified history can.
/// The fixture converts the seeded store to memory8/SQL5 and reopens atomically.
Future<void> runOfflineAuthorityBoundaryConformance({
  required Future<OfflineStore> Function() createStore,
  required Future<OfflineStore> Function(OfflineStore) migrateLegacy,
  required Future<OfflineStore> Function(OfflineStore) reopenStore,
  required AdmissionDurability durability,
}) async {
  var store = await createStore();
  final now = DateTime.utc(2026, 10, 1);
  final binding = _boundaryBinding();
  final scope = binding.stableIdentity;
  final epoch = ReceiptEpoch(Uint8List.fromList(List.filled(16, 1)));
  final receiptId = Uint8List.fromList(List.filled(49, 6))..[0] = 1;
  receiptId.setRange(1, 17, epoch.bytes);
  var timestamp = now.millisecondsSinceEpoch;
  for (var i = 24; i >= 17; i--) {
    receiptId[i] = timestamp & 255;
    timestamp >>= 8;
  }
  final receipt = OfflineReceiptEvidence(
    operationId: ReceiptOperationId(receiptId),
    groupId: ReceiptGroupId(Uint8List.fromList(List.filled(16, 5))),
    endpoint: ReceiptEndpoint(
      nodeId: Uint8List.fromList(List.filled(16, 2)),
      generation: Uint8List.fromList(List.filled(16, 3)),
    ),
    mutation: ReceiptMutationKind.edgeAdd,
    policy: OfflineReceiptPolicy(
      deploymentEpoch: epoch,
      retention: const Duration(hours: 24),
      maxEntries: BigInt.from(1024),
      maxBytes: BigInt.from(1 << 20),
      fingerprint: Uint8List.fromList(List.filled(32, 4)),
    ),
    itemIndex: 0,
    itemCount: 1,
    state: OfflineReceiptReconciliationState.lookupUnknown,
    mayHaveDispatched: true,
  );
  await store.transaction((tx) async {
    await tx.putCache(
      scope,
      OfflineCacheRecord.value(
        partitionId: scope,
        generation: 0,
        key: OfflineEntityKey.vertex('private'),
        entity: Vertex(
          key: 'private',
          value: VertexValue.string('old-authority'),
          expiration: now.add(const Duration(minutes: 2)),
        ),
        validatedAt: now,
        lastAccessAt: now,
      ),
    );
    final row = await tx.enqueue(
      OfflineOutboxRecord(
        recordId: 'legacy-add',
        operationId: 'legacy-operation',
        itemIndex: 0,
        partitionId: scope,
        intent: OfflineReceiptAddEdgeIntent(
          Edge(
            tail: 't',
            head: 'h',
            weight: .5,
            expiration: now.add(const Duration(minutes: 2)),
          ),
          Uint8List.fromList(List.filled(24, 8)),
        ),
        enqueuedAt: now,
        ordinal: 0,
        state: OfflineOutboxState.enqueued,
        attemptCount: 1,
        generation: 0,
        nextAttemptAt: now.add(const Duration(seconds: 1)),
        receipt: receipt,
      ),
    );
    await tx.putOperation(
      OfflineOperationRecord(
        partitionId: scope,
        generation: 0,
        operationId: row.operationId,
        items: [
          OfflineWriteStatus(
            recordId: row.recordId,
            operationId: row.operationId,
            itemIndex: 0,
            state: OfflineWriteState.retryScheduled,
            attemptCount: 1,
            mayHaveDispatched: true,
          ),
        ],
        updatedAt: now,
      ),
    );
  });
  final before = (await store.transaction((tx) => tx.outbox(scope))).single;
  store = await migrateLegacy(store);
  Future<OfflineLanternDataClient> open(String authority) =>
      OfflineLanternDataClient.open(
        client: LanternClient.connect(
          binding.serviceOrigin,
          allowInsecure: true,
          tokenProvider: () =>
              throw StateError('authority fixture must stay local'),
        ),
        store: store,
        binding: _boundaryBinding(authority),
        durability: durability,
        clock: () => now,
      );
  var client = await open('a');
  try {
    final hidden = await client.getVertex(
      'private',
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    _require(
      hidden.view == null && hidden.confirmed.state == ConfirmedState.unknown,
      'legacy unbound cache cannot inherit installed authority',
    );
    _require(
      await store.transaction(
        (tx) =>
            tx.hasUnknownResident(scope, OfflineEntityKey.vertex('private')),
      ),
      'bounded legacy resident retained for revalidation',
    );
    void evidence(OfflineOutboxRecord row) {
      _require(
        row.recordId == before.recordId &&
            row.intent.expiration == before.intent.expiration &&
            row.receipt!.operationId == before.receipt!.operationId &&
            row.receipt!.groupId == before.receipt!.groupId &&
            row.mayHaveDispatched &&
            (row.intent as OfflineReceiptAddEdgeIntent).contributionId.join(
                  ',',
                ) ==
                (before.intent as OfflineReceiptAddEdgeIntent).contributionId
                    .join(','),
        'authority transition preserves immutable possibly-sent evidence',
      );
    }

    evidence((await store.transaction((tx) => tx.outbox(scope))).single);
    await store.transaction(
      (tx) => tx.putCache(
        scope,
        OfflineCacheRecord.value(
          partitionId: scope,
          generation: 0,
          key: OfflineEntityKey.vertex('qualified'),
          entity: Vertex(
            key: 'qualified',
            value: VertexValue.string('revalidated'),
            expiration: now.add(const Duration(minutes: 1)),
          ),
          validatedAt: now,
          lastAccessAt: now,
        ),
      ),
    );
    await client.close();
    store = await reopenStore(store);
    client = await open('a');
    final retained = await client.getVertex(
      'qualified',
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    _require(
      retained.confirmed.state == ConfirmedState.present &&
          retained.view != null,
      'same-authority current-format reopen retains eligible history',
    );
    await client.close();
    client = await open('b');
    final changed = await client.getVertex(
      'qualified',
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    _require(
      changed.view == null && changed.confirmed.state == ConfirmedState.unknown,
      'changed authority hides old content',
    );
    evidence((await store.transaction((tx) => tx.outbox(scope))).single);
    await client.close();
    await store.transaction((tx) => tx.wipePartition(scope));
    _require(
      (await store.transaction((tx) => tx.outbox(scope))).isEmpty &&
          await store.transaction(
                (tx) => tx.getOperation(scope, before.operationId),
              ) ==
              null,
      'explicit wipe erases local receipt/result history without Server undo claim',
    );
  } finally {
    await client.close();
  }
}

VertexInput _vertex(String key, {Duration? ttl}) =>
    VertexInput(key: key, value: VertexValue.string('pending'), expiresIn: ttl);

void _require(bool ok, String label) {
  if (!ok) throw StateError('Offline mutation boundary conformance: $label');
}

final class _BoundaryFixture {
  _BoundaryFixture(this.store, this.sender);
  final OfflineStore store;
  final _BoundaryMutationRemote sender;
  DateTime now = DateTime.utc(2026, 10, 1);
  late final OfflineLanternDataClient client;

  static Future<_BoundaryFixture> open(
    OfflineStore store,
    AdmissionDurability durability,
  ) async {
    final f = _BoundaryFixture(store, _BoundaryMutationRemote());
    final endpoint = Uri.parse('http://127.0.0.1:1');
    f.client = await OfflineLanternDataClient.open(
      client: LanternClient.connect(
        endpoint,
        allowInsecure: true,
        tokenProvider: () => throw StateError('unexpected network'),
      ),
      store: store,
      binding: _boundaryBinding(),
      durability: durability,
      clock: () => f.now,
      mutationRemote: f.sender,
    );
    return f;
  }
}

LanternSessionBinding _boundaryBinding([String authority = 'a']) =>
    LanternSessionBinding.application(
      serviceOrigin: Uri.parse('http://127.0.0.1:1'),
      securityDomain: 'boundary-conformance',
      accountIdentity: 'one',
      authorityBinding: authority,
    );

final class _BoundaryMutationRemote implements OfflineBoundMutationRemote {
  Future<void> Function()? before, after;
  int calls = 0;
  @override
  Future<MutationReply<Object?>> sendBoundMutation(
    OfflineOutboxRecord record, {
    required void Function() beforeDispatch,
    required void Function() beforePublication,
    LanternCancellationToken? cancellation,
  }) async {
    await before?.call();
    beforeDispatch();
    calls++;
    await after?.call();
    beforePublication();
    return const MutationKnownEffect(PutOutcome.appliedAndLive);
  }
}

final class _BoundaryFaultStore implements OfflineStore {
  _BoundaryFaultStore(this.inner);
  final OfflineStore inner;
  bool failConfirmation = false, failReads = false;
  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction) action,
  ) {
    if (failReads) return Future.error(StateError('storage unavailable'));
    return inner.transaction((tx) async {
      final result = await action(tx);
      if (failConfirmation &&
          (await tx.operations(_scope)).any(
            (op) => op.items.any(
              (item) => item.state == OfflineWriteState.confirmed,
            ),
          )) {
        throw StateError('confirmation fault');
      }
      return result;
    });
  }

  String get _scope => LanternSessionBinding.application(
    serviceOrigin: Uri.parse('http://127.0.0.1:1'),
    securityDomain: 'boundary-conformance',
    accountIdentity: 'one',
    authorityBinding: 'a',
  ).stableIdentity;
  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      inner.changes(partitionId);
}
