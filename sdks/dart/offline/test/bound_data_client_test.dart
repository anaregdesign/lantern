import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';
import 'package:test/test.dart';
import 'helpers.dart';

final endpoint = Uri.parse('http://localhost:6380');
LanternSessionBinding binding([
  String account = 'one',
  String authority = 'a',
]) => LanternSessionBinding.application(
  serviceOrigin: endpoint,
  securityDomain: 'test',
  accountIdentity: account,
  authorityBinding: authority,
);
VertexInput vertex(String key, {Duration? ttl}) =>
    VertexInput(key: key, value: VertexValue.string('value'), expiresIn: ttl);
Future<OfflineLanternDataClient> open(
  OfflineStore store,
  MutableClock clock, {
  FakeOfflineRemote? remote,
  OfflineBoundMutationRemote? mutationRemote,
  TokenProvider? token,
  bool cache = true,
  bool Function()? authority,
}) => OfflineLanternDataClient.open(
  client: LanternClient.connect(
    endpoint,
    allowInsecure: true,
    clock: clock.call,
    transport: FakeTransportBuilder().build(),
    tokenProvider:
        token ?? () => throw StateError('token during local admission'),
  ),
  store: store,
  binding: binding(),
  durability: AdmissionDurability.memory,
  clock: clock.call,
  receiptRemote: remote,
  mutationRemote: mutationRemote,
  cacheReads: cache,
  authorityCurrent: authority,
);

final class MutationRemote implements OfflineBoundMutationRemote {
  MutationRemote(this.reply);
  MutationReply<Object?> reply;
  int calls = 0;
  Future<void> Function()? before, after;
  OfflineOutboxRecord? last;
  @override
  Future<MutationReply<Object?>> sendBoundMutation(
    OfflineOutboxRecord r, {
    required void Function() beforeDispatch,
    required void Function() beforePublication,
    LanternCancellationToken? cancellation,
  }) async {
    await before?.call();
    beforeDispatch();
    calls++;
    last = r;
    await after?.call();
    beforePublication();
    return reply;
  }
}

void main() {
  late MutableClock clock;
  setUp(() => clock = MutableClock(DateTime.utc(2026, 7, 22)));
  test(
    'memory8 migration hides unbound cache and preserves receipt evidence across authority/reopen',
    () async {
      await runOfflineAuthorityBoundaryConformance(
        createStore: () async => InMemoryOfflineStore(),
        migrateLegacy: (store) async {
          final data =
              jsonDecode(await (store as InMemoryOfflineStore).exportSnapshot())
                  as Map<String, dynamic>;
          data['schema'] = 8;
          for (final p in data['partitions'] as List) {
            p.remove('authorityBinding');
            p.remove('timeHighWater');
            p['outbox'] = [
              for (final raw in p['outbox'] as List)
                (() {
                  final r = jsonDecode(raw as String) as Map<String, dynamic>;
                  r['schema'] = 4;
                  r.remove('preparation');
                  r.remove('mayHaveDispatched');
                  r.remove('dependencies');
                  return jsonEncode(r);
                })(),
            ];
            p['operations'] = [
              for (final raw in p['operations'] as List)
                (() {
                  final r = jsonDecode(raw as String) as Map<String, dynamic>;
                  r['schema'] = 3;
                  r.remove('kind');
                  r.remove('guardUntil');
                  r.remove('groupId');
                  for (final item in r['items'] as List) {
                    item.remove('mayHaveDispatched');
                  }
                  return jsonEncode(r);
                })(),
            ];
          }
          return InMemoryOfflineStore.fromSnapshot(jsonEncode(data));
        },
        reopenStore: (store) async => InMemoryOfflineStore.fromSnapshot(
          await (store as InMemoryOfflineStore).exportSnapshot(),
        ),
        durability: AdmissionDurability.memory,
      );
    },
  );
  test(
    'memory claim expiry/reclaim, pending TTL and eligible storage fault counterexamples',
    () async {
      await runOfflineMutationBoundaryConformance(
        createStore: () async => MemoryOfflineStore(),
        durability: AdmissionDurability.memory,
      );
    },
  );
  test(
    'supported bounded memory profile passes the paired common conformance',
    () async {
      await runOfflineDataClientConformance(
        createStore: () async => MemoryOfflineStore(),
        reopenStore: (previous) async => previous,
        durability: AdmissionDurability.memory,
      );
    },
  );
  test(
    'every family admits without token, capability or RPC, with frozen TTL and Add IDs',
    () async {
      var tokens = 0;
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final client = await open(
        store,
        clock,
        remote: remote,
        token: () {
          tokens++;
          throw StateError('token');
        },
      );
      addTearDown(client.close);
      final requests = <MutationRequest<Object?>>[
        MutationRequest.putVertices([
          vertex('v', ttl: const Duration(seconds: 10)),
        ]),
        MutationRequest.putVerticesIfAbsent([vertex('c')]),
        MutationRequest.putEdges([EdgeInput(tail: 'v', head: 'c', weight: .1)]),
        MutationRequest.addEdges([EdgeInput(tail: 'v', head: 'c', weight: .1)]),
        MutationRequest.deleteVertices(['v', 'v', 'missing']),
        MutationRequest.deleteEdges([const EdgeRef('v', 'c')]),
        MutationRequest.deleteEdgeContributions([
          EdgeContributionRef(
            tail: 'v',
            head: 'c',
            contribId: Uint8List.fromList(List.filled(24, 1)),
          ),
        ]),
      ];
      for (final r in requests) {
        final t = await client.mutations.submit(r);
        expect(t.durability, AdmissionDurability.memory);
      }
      expect(tokens, 0);
      expect(remote.receiptCalls, isEmpty);
      final rows = await store.transaction(
        (tx) => tx.outbox(binding().stableIdentity),
      );
      expect(rows.length, 9);
      expect(
        rows.first.intent.expiration,
        clock.now.add(const Duration(seconds: 10)),
      );
      expect(
        rows.where((r) => r.preparation is OfflineAwaitingPreparation).length,
        7,
      );
      final add =
          rows.firstWhere((r) => r.intent is OfflineReceiptAddEdgeIntent).intent
              as OfflineReceiptAddEdgeIntent;
      expect(add.contributionId.length, 24);
      expect(add.contributionId.any((v) => v != 0), isTrue);
      final restored = InMemoryOfflineStore.fromSnapshot(
        await store.exportSnapshot(),
      );
      final reopened = await restored.transaction(
        (tx) => tx.outbox(binding().stableIdentity),
      );
      expect(
        OfflineCodec.encodeOutboxRecord(reopened[3]),
        OfflineCodec.encodeOutboxRecord(rows[3]),
      );
    },
  );
  test(
    'ordinary Future remains pending and postcommit waiter cancellation detaches only',
    () async {
      final store = InMemoryOfflineStore();
      final client = await open(store, clock);
      addTearDown(client.close);
      final cancel = LanternCancellationToken();
      var settled = false;
      final waiting = client
          .putVertex(
            vertex('v'),
            options: MutationOptions(wait: WaitOptions(cancellation: cancel)),
          )
          .whenComplete(() => settled = true);
      final assertion = expectLater(
        waiting,
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.waitCanceled,
          ),
        ),
      );
      await Future<void>.delayed(Duration.zero);
      await Future<void>.delayed(Duration.zero);
      expect(settled, isFalse);
      cancel.cancel();
      await assertion;
      final rows = await store.transaction(
        (tx) => tx.outbox(binding().stableIdentity),
      );
      expect(rows.length, 1);
      expect(rows.single.mayHaveDispatched, isFalse);
    },
  );
  test(
    'Put Add Delete condition and later exact overwrite fold the whole sequence',
    () async {
      final store = InMemoryOfflineStore();
      final client = await open(store, clock);
      addTearDown(client.close);
      final key = OfflineEntityKey.edge('t', 'h');
      await store.transaction(
        (tx) async => tx.putCache(
          binding().stableIdentity,
          OfflineCacheRecord.value(
            partitionId: binding().stableIdentity,
            generation: 0,
            key: key,
            entity: Edge(tail: 't', head: 'h', weight: 7, expiration: null),
            validatedAt: clock.now,
            lastAccessAt: clock.now,
          ),
        ),
      );
      await client.mutations.submit(
        MutationRequest.putEdges([EdgeInput(tail: 't', head: 'h', weight: 2)]),
      );
      var s = await client.getEdge(
        const EdgeRef('t', 'h'),
        options: const GetOptions(source: ReadSource.cacheOnly),
      );
      expect(s.view!.weight, 2);
      expect(s.confirmed.value!.weight, 7);
      expect(s.projection, PendingProjection.exactPendingPut);
      await client.mutations.submit(
        MutationRequest.addEdges([EdgeInput(tail: 't', head: 'h', weight: 3)]),
      );
      s = await client.getEdge(
        const EdgeRef('t', 'h'),
        options: const GetOptions(source: ReadSource.cacheOnly),
      );
      expect(s.view!.weight, 7);
      expect(s.pending.count, 2);
      expect(s.pending.wholeSequenceExact, isFalse);
      await client.mutations.submit(
        MutationRequest.deleteEdges([const EdgeRef('t', 'h')]),
      );
      s = await client.getEdge(const EdgeRef('t', 'h'));
      expect(s.view, isNull);
      expect(s.confirmed.state, ConfirmedState.present);
      expect(s.projection, PendingProjection.pendingDelete);
      expect(s.pending.wholeSequenceExact, isTrue);
      await client.mutations.submit(MutationRequest.putVertices([vertex('v')]));
      await client.mutations.submit(MutationRequest.deleteVertices(['v']));
      await client.mutations.submit(
        MutationRequest.putVerticesIfAbsent([vertex('v')]),
      );
      final v = await client.getVertex(
        'v',
        options: const GetOptions(source: ReadSource.cacheOnly),
      );
      expect(v.projection, PendingProjection.unresolvedPending);
      expect(v.view, isNull);
      expect(v.confirmed.state, ConfirmedState.unknown);
    },
  );
  test(
    'explicit ID collision and aged reuse cannot reopen after clock rollback',
    () async {
      final store = InMemoryOfflineStore();
      final client = await open(store, clock);
      addTearDown(client.close);
      final id = OperationId.generate(issuedAt: clock.now);
      await client.mutations.submit(
        MutationRequest.deleteVertices(['v']),
        options: MutationOptions(operationId: id),
      );
      await expectLater(
        client.mutations.submit(
          MutationRequest.deleteVertices(['v']),
          options: MutationOptions(operationId: id),
        ),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.operationIdCollision,
          ),
        ),
      );
      clock.advance(const Duration(minutes: 6));
      await client.getVertex(
        'v',
        options: const GetOptions(source: ReadSource.cacheOnly),
      );
      clock.advance(const Duration(minutes: -6));
      await expectLater(
        client.mutations.submit(
          MutationRequest.deleteVertices(['v']),
          options: MutationOptions(operationId: id),
        ),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.operationIdExpired,
          ),
        ),
      );
    },
  );
  test(
    'capacity cancellation and born-expired Put reject atomically',
    () async {
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxOutboxRecords: 1,
          maxOutboxRecordsPerPartition: 1,
        ),
      );
      final client = await open(store, clock);
      addTearDown(client.close);
      await expectLater(
        client.mutations.submit(MutationRequest.deleteVertices(['a', 'b'])),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        await store.transaction((tx) => tx.outbox(binding().stableIdentity)),
        isEmpty,
      );
      final cancel = LanternCancellationToken()..cancel();
      await expectLater(
        client.mutations.submit(
          MutationRequest.deleteVertices(['v']),
          options: MutationOptions(admissionCancellation: cancel),
        ),
        throwsA(isA<OfflineCanceledException>()),
      );
      await expectLater(
        client.mutations.submit(
          MutationRequest.putVertices([
            VertexInput(
              key: 'v',
              value: VertexValue.nil(),
              expiresAt: clock.now,
            ),
          ]),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      expect(
        await store.transaction(
          (tx) => tx.operations(binding().stableIdentity),
        ),
        isEmpty,
      );
    },
  );
  test(
    'explicit reverse dependency plus implicit FIFO rejects atomically',
    () async {
      final store = InMemoryOfflineStore();
      final client = await open(store, clock);
      addTearDown(client.close);
      await expectLater(
        client.mutations.submitGroup(
          MutationGroup([
            MutationGroupNode(
              MutationRequest.putVertices([vertex('v')]),
              dependencies: [const MutationDependency(node: 1, item: 0)],
            ),
            MutationGroupNode(MutationRequest.deleteVertices(['v'])),
          ]),
        ),
        throwsA(isA<OfflineDurableGraphException>()),
      );
      expect(
        await store.transaction((tx) => tx.outbox(binding().stableIdentity)),
        isEmpty,
      );
    },
  );
  test(
    'deferred preparation commits before send and exact late Put remains original',
    () async {
      final remote = FakeOfflineRemote();
      final store = InMemoryOfflineStore();
      final sending = MutationRemote(
        const MutationKnownEffect(PutOutcome.appliedAndLive),
      );
      final client = await open(
        store,
        clock,
        remote: remote,
        mutationRemote: sending,
      );
      addTearDown(client.close);
      final ticket = await client.mutations.submit(
        MutationRequest.putVertices([
          vertex('v', ttl: const Duration(seconds: 1)),
        ]),
      );
      sending.after = () async => clock.advance(const Duration(seconds: 2));
      final result = client.mutations.reobserve(ticket.reference);
      expect(await client.mutationDelivery.dispatchPass(), 1);
      expect(
        (await result).reply!.items.single,
        isA<MutationKnownEffect<PutOutcome>>().having(
          (r) => r.effect,
          'effect',
          PutOutcome.appliedAndLive,
        ),
      );
      expect(
        (await client.mutations.getStatus(
          ticket.reference.operationId,
        )).items.single.dispatchCertainty,
        MutationDispatchCertainty.mayHaveDispatched,
      );
      expect(
        await store.transaction((tx) => tx.outbox(binding().stableIdentity)),
        isEmpty,
      );
    },
  );
  test(
    'receipt response loss reopens status-first with immutable receipt and Add IDs',
    () async {
      final remote = FakeOfflineRemote()
        ..commitBeforeReceiptSendFailure = true
        ..receiptSendFailures.add(
          failure(OfflineRemoteErrorKind.outcomeUnknown),
        );
      final store = InMemoryOfflineStore();
      final sender = ReceiptMutationRemote(remote);
      final client = await open(
        store,
        clock,
        remote: remote,
        mutationRemote: sender,
      );
      final ticket = await client.mutations.submit(
        MutationRequest.addEdges([EdgeInput(tail: 't', head: 'h', weight: .5)]),
      );
      expect(await client.mutationDelivery.dispatchPass(), 0);
      final sent = (await store.transaction(
        (tx) => tx.outbox(binding().stableIdentity),
      )).single;
      expect(sent.mayHaveDispatched, isTrue);
      expect(remote.receiptSendCalls, 1);
      final originalId = sent.receipt!.operationId,
          originalAdd =
              (sent.intent as OfflineReceiptAddEdgeIntent).contributionId;
      final snapshot = await store.exportSnapshot();
      await client.close();
      clock.advance(const Duration(seconds: 2));
      final restored = InMemoryOfflineStore.fromSnapshot(snapshot);
      final reopened = await open(
        restored,
        clock,
        remote: remote,
        mutationRemote: sender,
      );
      addTearDown(reopened.close);
      final observation = reopened.mutations.reobserve(ticket.reference);
      expect(await reopened.mutationDelivery.dispatchPass(), 1);
      expect(
        (await observation).reply!.items.single,
        isA<MutationKnownEffect<double>>().having(
          (v) => v.effect,
          'effect',
          .5,
        ),
      );
      expect(remote.receiptSendCalls, 1);
      expect(remote.receiptStatusIds.last, originalId);
      expect(
        (sender.last!.intent as OfflineReceiptAddEdgeIntent).contributionId,
        originalAdd,
      );
    },
  );
  for (final weight in [double.nan, double.infinity, double.negativeInfinity]) {
    test(
      'exact nonfinite Add handling survives operation codec: $weight',
      () async {
        final remote = FakeOfflineRemote()
          ..receiptSendResults.add(OfflineEdgeAddReceiptResult(weight));
        final store = InMemoryOfflineStore();
        final client = await open(
          store,
          clock,
          remote: remote,
          mutationRemote: ReceiptMutationRemote(remote),
        );
        addTearDown(client.close);
        final ticket = await client.mutations.submit(
          MutationRequest.addEdges([
            EdgeInput(tail: 't', head: 'h', weight: 1),
          ]),
        );
        expect(await client.mutationDelivery.dispatchPass(), 1);
        final restored = InMemoryOfflineStore.fromSnapshot(
          await store.exportSnapshot(),
        );
        final after = await open(
          restored,
          clock,
          remote: remote,
          mutationRemote: ReceiptMutationRemote(remote),
        );
        addTearDown(after.close);
        final result =
            (await after.mutations.reobserve(
                  ticket.reference,
                )).reply!.items.single
                as MutationKnownEffect<double>;
        expect(
          weight.isNaN ? result.effect.isNaN : result.effect == weight,
          isTrue,
        );
      },
    );
  }
  test(
    'false Delete is exact and failed explicit prerequisite prevents dependent dispatch',
    () async {
      final remote = FakeOfflineRemote()
        ..receiptSendResults.add(const OfflineVertexDeleteReceiptResult(false));
      final store = InMemoryOfflineStore();
      final sender = ReceiptMutationRemote(remote);
      final client = await open(
        store,
        clock,
        remote: remote,
        mutationRemote: sender,
      );
      addTearDown(client.close);
      final tickets = await client.mutations.submitGroup(
        MutationGroup([
          MutationGroupNode(MutationRequest.deleteVertices(['v'])),
          MutationGroupNode(
            MutationRequest.putVertices([vertex('dependent')]),
            dependencies: [
              const MutationDependency(
                node: 0,
                item: 0,
                predicate: MutationDependencyPredicate.deleteExisted,
              ),
            ],
          ),
          MutationGroupNode(MutationRequest.putVertices([vertex('successor')])),
        ]),
      );
      // Independent work progresses while the explicit dependent is held.
      await client.mutationDelivery.dispatchPass();
      clock.advance(const Duration(seconds: 2));
      await client.mutationDelivery.dispatchPass();
      final first = await client.mutations.getStatus(
        tickets[0].reference.operationId,
      );
      expect(
        (first.items.single.reply as MutationKnownEffect<Object?>).effect,
        false,
      );
      final dep = await client.mutations.getStatus(
        tickets[1].reference.operationId,
      );
      expect(dep.terminal, isTrue);
      expect(dep.items.single.state, MutationItemState.failed);
      expect(
        (dep.items.single.failure! as LanternDataException).reason,
        DataFailure.dependencyFailed,
      );
      expect(remote.receiptSendCalls, 1);
      expect(sender.plainCalls, 1);
    },
  );
  for (final failAllReads in [false, true]) {
    test(
      'valid reply settles live waiter with confirmation fault (all reads fail: $failAllReads)',
      () async {
        final raw = InMemoryOfflineStore();
        final store = FaultStore(raw);
        final remote = FakeOfflineRemote();
        final sender = MutationRemote(
          const MutationKnownEffect(PutOutcome.conditionNotMet),
        );
        final client = await open(
          store,
          clock,
          remote: remote,
          mutationRemote: sender,
        );
        addTearDown(client.close);
        final ticket = await client.mutations.submit(
          MutationRequest.putVerticesIfAbsent([vertex('v')]),
          options: const MutationOptions(wait: WaitOptions()),
        );
        sender.after = () async {
          store.failConfirmation = true;
          store.failAll = failAllReads;
        };
        final observer = ticket.completion!;
        await expectLater(
          client.mutationDelivery.dispatchPass(),
          throwsA(isA<StateError>()),
        );
        expect(
          (await observer).reply!.items.single,
          isA<MutationKnownEffect<PutOutcome>>().having(
            (r) => r.effect,
            'effect',
            PutOutcome.conditionNotMet,
          ),
        );
        expect(client.mutationDelivery.storageBlocked, isTrue);
        final retained = (await raw.transaction(
          (tx) => tx.outbox(binding().stableIdentity),
        )).single;
        expect(retained.mayHaveDispatched, isTrue);
        expect(retained.receipt, isNotNull);
        expect(
          (await raw.transaction(
            (tx) =>
                tx.getOperation(binding().stableIdentity, retained.operationId),
          ))!.items.single.state,
          OfflineWriteState.sending,
        );
        await expectLater(
          client.mutations.submit(MutationRequest.deleteVertices(['new'])),
          throwsA(
            isA<LanternDataException>().having(
              (e) => e.reason,
              'reason',
              DataFailure.storageBlocked,
            ),
          ),
        );
      },
    );
  }
  test(
    'undisclosed handling satisfies handled only and releases ordinary FIFO',
    () async {
      final store = InMemoryOfflineStore();
      final sender = MutationRemote(
        const MutationAcceptedUndisclosed<PutOutcome>(),
      );
      final client = await open(
        store,
        clock,
        remote: FakeOfflineRemote(),
        mutationRemote: sender,
      );
      addTearDown(client.close);
      final tickets = await client.mutations.submitGroup(
        MutationGroup([
          MutationGroupNode(MutationRequest.putVertices([vertex('source')])),
          MutationGroupNode(MutationRequest.putVertices([vertex('source')])),
          MutationGroupNode(
            MutationRequest.putVertices([vertex('handled')]),
            dependencies: [
              const MutationDependency(
                node: 0,
                item: 0,
                predicate: MutationDependencyPredicate.handled,
              ),
            ],
          ),
          MutationGroupNode(
            MutationRequest.putVertices([vertex('known')]),
            dependencies: [const MutationDependency(node: 0, item: 0)],
          ),
        ]),
      );
      await client.mutationDelivery.dispatchPass();
      clock.advance(const Duration(seconds: 2));
      await client.mutationDelivery.dispatchPass();
      final observation = await client.mutations.reobserve(
        tickets.first.reference,
      );
      expect(
        observation.reply!.items.single,
        isA<MutationAcceptedUndisclosed<Object?>>(),
      );
      expect(
        (await client.mutations.getStatus(
          tickets[1].reference.operationId,
        )).items.single.state,
        MutationItemState.handled,
      );
      expect(
        (await client.mutations.getStatus(
          tickets[2].reference.operationId,
        )).items.single.state,
        MutationItemState.handled,
      );
      final failure = await client.mutations.getStatus(
        tickets[3].reference.operationId,
      );
      expect(
        (failure.items.single.failure as LanternDataException).reason,
        DataFailure.dependencyFailed,
      );
      expect(sender.calls, 3);
      final status = await client.syncStatus.firstWhere(
        (s) => s.terminalAttentionItems == 1,
      );
      expect(status.activeMutations, 0);
    },
  );
  test(
    'never-dispatched expired conditional intent terminalizes without preparation or token',
    () async {
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final client = await open(store, clock, remote: remote);
      addTearDown(client.close);
      final ticket = await client.mutations.submit(
        MutationRequest.putVerticesIfAbsent([
          vertex('v', ttl: const Duration(seconds: 1)),
        ]),
      );
      clock.advance(const Duration(seconds: 2));
      final observing = client.mutations.reobserve(ticket.reference);
      final assertion = expectLater(
        observing,
        throwsA(
          isA<MutationBatchException<PutOutcome>>().having(
            (e) => (e.cause as LanternDataException).reason,
            'reason',
            DataFailure.expiredBeforeDispatch,
          ),
        ),
      );
      expect(await client.mutationDelivery.dispatchPass(), 0);
      await assertion;
      expect(remote.receiptCalls, isEmpty);
      expect(
        (await client.mutations.getStatus(
          ticket.reference.operationId,
        )).items.single.dispatchCertainty,
        MutationDispatchCertainty.neverDispatched,
      );
    },
  );
  test(
    'authority change in asynchronous token gap cannot send or publish after pre-send commit',
    () async {
      var current = true;
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final sender = MutationRemote(
        const MutationKnownEffect(PutOutcome.appliedAndLive),
      );
      final client = await open(
        store,
        clock,
        remote: remote,
        mutationRemote: sender,
        authority: () => current,
      );
      addTearDown(client.close);
      await client.mutations.submit(MutationRequest.putVertices([vertex('v')]));
      sender.before = () async {
        current = false;
      };
      await expectLater(
        client.mutationDelivery.dispatchPass(),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.closed,
          ),
        ),
      );
      expect(sender.calls, 0);
      final row = (await store.transaction(
        (tx) => tx.outbox(binding().stableIdentity),
      )).single;
      expect(row.mayHaveDispatched, isTrue);
      expect(row.attemptCount, 1);
    },
  );
  test(
    'stale delayed preparation retains fixed intent and spends no send attempt',
    () async {
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      remote.beforeReceiptPreparationReturn = () async =>
          clock.advance(const Duration(minutes: 5));
      final client = await open(
        store,
        clock,
        remote: remote,
        mutationRemote: ReceiptMutationRemote(remote),
      );
      addTearDown(client.close);
      await client.mutations.submit(
        MutationRequest.addEdges([EdgeInput(tail: 't', head: 'h', weight: 1)]),
      );
      final original =
          (await store.transaction(
                (tx) => tx.outbox(binding().stableIdentity),
              )).single.intent
              as OfflineReceiptAddEdgeIntent;
      expect(await client.mutationDelivery.dispatchPass(), 0);
      final row = (await store.transaction(
        (tx) => tx.outbox(binding().stableIdentity),
      )).single;
      expect(row.preparation, isA<OfflineAwaitingPreparation>());
      expect(row.attemptCount, 0);
      expect(row.mayHaveDispatched, isFalse);
      expect(
        (row.intent as OfflineReceiptAddEdgeIntent).contributionId,
        original.contributionId,
      );
      expect(remote.receiptSendCalls, 0);
    },
  );
  test(
    'terminal ID guard prevents capacity eviction before the full admission window',
    () async {
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxOperationRecords: 1,
          maxOperationRecordsPerPartition: 1,
        ),
      );
      final sender = MutationRemote(
        const MutationKnownEffect(PutOutcome.appliedAndLive),
      );
      final client = await open(
        store,
        clock,
        remote: FakeOfflineRemote(),
        mutationRemote: sender,
      );
      addTearDown(client.close);
      final ticket = await client.mutations.submit(
        MutationRequest.putVertices([vertex('v')]),
      );
      expect(await client.mutationDelivery.dispatchPass(), 1);
      clock.advance(const Duration(seconds: 2));
      await expectLater(
        client.mutations.submit(MutationRequest.putVertices([vertex('next')])),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        (await client.mutations.getStatus(
          ticket.reference.operationId,
        )).retention,
        MutationRetention.retained,
      );
      clock.advance(const Duration(minutes: 6));
      await client.mutations.submit(
        MutationRequest.putVertices([vertex('next')]),
      );
      expect(
        (await client.mutations.reobserve(ticket.reference)).retention,
        MutationRetention.evicted,
      );
      await expectLater(
        client.mutations.submit(
          MutationRequest.putVertices([vertex('v')]),
          options: MutationOptions(operationId: ticket.reference.operationId),
        ),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.operationIdExpired,
          ),
        ),
      );
    },
  );
}

final class ReceiptMutationRemote implements OfflineBoundMutationRemote {
  ReceiptMutationRemote(this.remote);
  final FakeOfflineRemote remote;
  OfflineOutboxRecord? last;
  int plainCalls = 0;
  @override
  Future<MutationReply<Object?>> sendBoundMutation(
    OfflineOutboxRecord record, {
    required void Function() beforeDispatch,
    required void Function() beforePublication,
    LanternCancellationToken? cancellation,
  }) async {
    beforeDispatch();
    last = record;
    if (record.receipt == null) {
      plainCalls++;
      beforePublication();
      return const MutationKnownEffect(PutOutcome.appliedAndLive);
    }
    final result = await remote.sendReceiptMutation(
      record.intent,
      context: record.receipt!.context,
      cancellation: cancellation,
    );
    beforePublication();
    return offlineMutationReply<Object?>(
      OfflineWriteStatus(
        recordId: record.recordId,
        operationId: record.operationId,
        itemIndex: record.itemIndex,
        state: OfflineWriteState.confirmed,
        attemptCount: record.attemptCount,
        receiptResult: result,
      ),
    )!;
  }
}

final class FaultStore implements OfflineStore {
  FaultStore(this.inner);
  final InMemoryOfflineStore inner;
  bool failConfirmation = false;
  bool failAll = false;
  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction) action,
  ) {
    if (failAll) return Future.error(StateError('storage unavailable'));
    return inner.transaction((tx) async {
      final value = await action(tx);
      if (failConfirmation &&
          (await tx.operations(binding().stableIdentity)).any(
            (o) => o.items.any((i) => i.state == OfflineWriteState.confirmed),
          )) {
        throw StateError('confirmation commit fault');
      }
      return value;
    });
  }

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      inner.changes(partitionId);
}
