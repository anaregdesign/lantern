import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

import 'helpers.dart';

void main() {
  final initial = DateTime.utc(2026, 7, 22, 12);

  for (final asynchronous in [false, true]) {
    test(
      'local Put overlays are immediately visible and replay exactly (async: $asynchronous)',
      () async {
        final clock = MutableClock(initial);
        final reference = InMemoryOfflineStore();
        final OfflineStore store = asynchronous
            ? DelayedOfflineStore(reference)
            : reference;
        final remote = FakeOfflineRemote();
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: testConfig(clock),
        );
        final put = await repository.putVertex(
          partitionId: 'p',
          input: VertexInput(key: 'v', value: VertexValue.string('local')),
        );
        final snapshot = await repository.readVertex(
          'p',
          'v',
          policy: OfflineReadPolicy.cacheOnly,
        );
        expect(snapshot.value!.value, isA<StringValue>());
        expect(snapshot.hasPendingWrites, isTrue);
        expect(await put.statuses.first, isA<OfflineWriteStatus>());

        await repository.putEdge(
          partitionId: 'p',
          input: EdgeInput(tail: 'a', head: 'b', weight: 0.1),
        );
        final edge = await repository.readEdge(
          'p',
          const EdgeRef('a', 'b'),
          policy: OfflineReadPolicy.cacheOnly,
        );
        expect(edge.hasPendingWrites, isTrue);
        expect(edge.value!.weight, Float32Value(0.1).value);
        final records = await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        );
        expect(records, hasLength(2));
        expect(await repository.listPending('p'), hasLength(2));
        expect(await repository.drain('p'), 2);
        expect(remote.edgePutCalls, 1);
        expect((await put.statuses.first).state, OfflineWriteState.confirmed);
        expect(
          (await repository.readVertex(
            'p',
            'v',
            policy: OfflineReadPolicy.cacheOnly,
          )).hasPendingWrites,
          isFalse,
        );
        expect(
          (await repository.readEdge(
            'p',
            const EdgeRef('a', 'b'),
            policy: OfflineReadPolicy.cacheOnly,
          )).state,
          OfflineReadState.fresh,
        );
        await repository.wipePartition('p');
        expect(
          await store.transaction(
            (transaction) async => await transaction.generation('p'),
          ),
          1,
        );
        expect(
          (await repository.readVertex(
            'p',
            'v',
            policy: OfflineReadPolicy.cacheOnly,
          )).state,
          OfflineReadState.unknown,
        );
        await repository.dispose();
      },
    );
  }

  group('receipt reconciliation', () {
    test('preparation and status reject mixed receipt metadata', () async {
      final capability = offlineReceiptCapability();
      final operationId = testReceiptOperationId(
        epoch: capability.policy.deploymentEpoch,
        random: 1,
      );
      final prepared = await FakeOfflineRemote().prepareReceipts(
        ReceiptMutationKind.edgeDelete,
        itemCount: 1,
      );
      final evidence = prepared.evidence.single;
      expect(
        () => OfflineReceiptPreparation(
          mutation: ReceiptMutationKind.edgeDelete,
          capability: capability,
          evidence: <OfflineReceiptEvidence>[evidence, evidence],
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(
        () => OfflineReceiptPreparation(
          mutation: ReceiptMutationKind.edgeDelete,
          capability: capability,
          evidence: <OfflineReceiptEvidence>[
            evidence.copyWith(
              state: OfflineReceiptReconciliationState.notYetObserved,
            ),
          ],
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(
        () => OfflineReceiptStatus(
          operationId: operationId,
          state: ReceiptStatusState.notYetObserved,
          groupId: ReceiptGroupId(testBytes(16, 22)),
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(
        () => OfflineReceiptStatus(
          operationId: operationId,
          state: ReceiptStatusState.confirmed,
          groupId: ReceiptGroupId(testBytes(16, 22)),
          mutation: ReceiptMutationKind.edgeDelete,
          itemIndex: 0,
          itemCount: 1,
          result: const OfflineVertexDeleteReceiptResult(true),
        ),
        throwsA(isA<OfflineArgumentException>()),
      );

      final mapped = mapLanternClientFailure(
        ReceiptReconciliationException(
          reason: ReceiptReconciliationReason.outcomeUnknown,
          context: evidence.context,
          message: 'response was not observed',
        ),
      );
      expect(
        mapped,
        isA<OfflineRemoteFailure>().having(
          (failure) => failure.kind,
          'kind',
          OfflineRemoteErrorKind.outcomeUnknown,
        ),
      );
    });

    test(
      'persists one-item contexts and exact plural results before send',
      () async {
        final clock = MutableClock(initial);
        final store = InMemoryOfflineStore();
        final remote = FakeOfflineRemote()
          ..receiptSendResults.addAll(const <OfflineReceiptResult>[
            OfflineVertexPutReceiptResult(PutOutcome.conditionNotMet),
            OfflineVertexPutReceiptResult(PutOutcome.expired),
            OfflineVertexPutReceiptResult(PutOutcome.appliedAndLive),
            OfflineVertexPutReceiptResult(PutOutcome.superseded),
          ]);
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: OfflineConfig(
            clock: clock.call,
            idGenerator: testConfig(clock).idGenerator,
            jitter: (_) => Duration.zero,
            maxConcurrency: 1,
            maxConcurrencyPerPartition: 1,
          ),
        );
        addTearDown(repository.dispose);

        final operation = await repository.putVerticesIfAbsent(
          partitionId: 'p',
          operationId: 'conditional',
          inputs: <VertexInput>[
            VertexInput(key: 'first', value: VertexValue.string('first')),
            VertexInput(key: 'second', value: VertexValue.string('second')),
            VertexInput(key: 'third', value: VertexValue.string('third')),
            VertexInput(key: 'fourth', value: VertexValue.string('fourth')),
          ],
        );

        expect(operation.items.map((item) => item.itemIndex), <int>[
          0,
          1,
          2,
          3,
        ]);
        expect(remote.receiptCalls, <String>['prepare']);
        expect(remote.receiptSendCalls, 0);
        final persisted = await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        );
        expect(persisted, hasLength(4));
        expect(
          persisted.map((record) => record.receipt!.operationId).toSet(),
          hasLength(4),
        );
        expect(
          persisted.map((record) => record.receipt!.groupId).toSet(),
          hasLength(4),
        );
        for (final record in persisted) {
          expect(record.receipt!.itemIndex, 0);
          expect(record.receipt!.itemCount, 1);
          expect(
            record.receipt!.state,
            OfflineReceiptReconciliationState.statusRequired,
          );
          expect(record.receipt!.endpoint.nodeId, testBytes(16, 12));
          expect(record.receipt!.endpoint.generation, testBytes(16, 13));
          expect(record.attemptCount, 0);
        }

        expect(await repository.drain('p'), 4);
        expect(remote.receiptSendCalls, 4);
        expect(remote.receiptStatusCalls, 4);
        expect(remote.receiptCapabilityCalls, 8);
        final status = await repository.getWriteStatus('p', 'conditional');
        expect(status!.items.map((item) => item.state), [
          OfflineWriteState.confirmed,
          OfflineWriteState.confirmed,
          OfflineWriteState.confirmed,
          OfflineWriteState.confirmed,
        ]);
        expect(
          (status.items[0].receiptResult as OfflineVertexPutReceiptResult)
              .outcome,
          PutOutcome.conditionNotMet,
        );
        expect(
          (status.items[1].receiptResult as OfflineVertexPutReceiptResult)
              .outcome,
          PutOutcome.expired,
        );
        expect(
          (status.items[2].receiptResult as OfflineVertexPutReceiptResult)
              .outcome,
          PutOutcome.appliedAndLive,
        );
        expect(
          (status.items[3].receiptResult as OfflineVertexPutReceiptResult)
              .outcome,
          PutOutcome.superseded,
        );
        expect(await repository.listPending('p'), isEmpty);
      },
    );

    test(
      'retains exact true and false Delete results in caller order',
      () async {
        final clock = MutableClock(initial);
        final remote = FakeOfflineRemote()
          ..receiptSendResults.addAll(const <OfflineReceiptResult>[
            OfflineVertexDeleteReceiptResult(true),
            OfflineVertexDeleteReceiptResult(false),
            OfflineEdgeDeleteReceiptResult(true),
            OfflineEdgeDeleteReceiptResult(false),
          ]);
        final repository = OfflineLanternRepository(
          store: InMemoryOfflineStore(),
          remote: remote,
          config: OfflineConfig(
            clock: clock.call,
            idGenerator: testConfig(clock).idGenerator,
            jitter: (_) => Duration.zero,
            maxConcurrency: 1,
            maxConcurrencyPerPartition: 1,
          ),
        );
        addTearDown(repository.dispose);

        final vertices = await repository.deleteVertices(
          partitionId: 'p',
          operationId: 'vertices',
          keys: <String>['existing', 'missing'],
        );
        expect(vertices.items.map((item) => item.itemIndex), <int>[0, 1]);
        expect(await repository.drain('p'), 2);
        final vertexStatus = await repository.getWriteStatus('p', 'vertices');
        expect(
          vertexStatus!.items.map(
            (item) => (item.receiptResult as OfflineVertexDeleteReceiptResult)
                .existed,
          ),
          <bool>[true, false],
        );

        final edges = await repository.deleteEdges(
          partitionId: 'p',
          operationId: 'edges',
          edges: const <EdgeRef>[
            EdgeRef('tail', 'existing'),
            EdgeRef('tail', 'missing'),
          ],
        );
        expect(edges.items.map((item) => item.itemIndex), <int>[0, 1]);
        expect(await repository.drain('p'), 2);
        final edgeStatus = await repository.getWriteStatus('p', 'edges');
        expect(
          edgeStatus!.items.map(
            (item) =>
                (item.receiptResult as OfflineEdgeDeleteReceiptResult).existed,
          ),
          <bool>[true, false],
        );
        expect(
          remote.receiptSendContexts.every(
            (context) => context.operationIds.length == 1,
          ),
          isTrue,
        );
      },
    );

    test(
      'persists contribution-keyed Add before send and retains exact weights',
      () async {
        final clock = MutableClock(initial);
        final store = InMemoryOfflineStore();
        final remote = FakeOfflineRemote()
          ..receiptSendResults.addAll(<OfflineReceiptResult>[
            OfflineEdgeAddReceiptResult(2),
            OfflineEdgeAddReceiptResult(double.infinity),
          ]);
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: OfflineConfig(
            clock: clock.call,
            idGenerator: testConfig(clock).idGenerator,
            jitter: (_) => Duration.zero,
            maxConcurrency: 1,
            maxConcurrencyPerPartition: 1,
          ),
        );
        addTearDown(repository.dispose);
        final firstContribution = testBytes(24, 31);
        final secondContribution = testBytes(24, 32);

        final operation = await repository.addEdges(
          partitionId: 'p',
          operationId: 'add-edges',
          inputs: <EdgeInput>[
            EdgeInput(
              tail: 'tail',
              head: 'head',
              weight: 2,
              contribId: firstContribution,
            ),
            EdgeInput(
              tail: 'tail',
              head: 'head',
              weight: 3,
              contribId: secondContribution,
            ),
          ],
        );
        firstContribution[0] = 0;
        secondContribution[0] = 0;

        expect(operation.items.map((item) => item.itemIndex), <int>[0, 1]);
        expect(remote.receiptCalls, <String>['prepare']);
        expect(remote.receiptSendCalls, 0);
        final persisted = await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        );
        expect(persisted, hasLength(2));
        expect(
          persisted.map((record) => record.receipt!.mutation),
          everyElement(ReceiptMutationKind.edgeAdd),
        );
        expect(
          (persisted[0].intent as OfflineReceiptAddEdgeIntent).contributionId,
          testBytes(24, 31),
        );
        expect(
          (persisted[1].intent as OfflineReceiptAddEdgeIntent).contributionId,
          testBytes(24, 32),
        );
        final exposed =
            (persisted[0].intent as OfflineReceiptAddEdgeIntent).contributionId
              ..[0] = 0;
        expect(exposed.first, 0);
        expect(
          (persisted[0].intent as OfflineReceiptAddEdgeIntent)
              .contributionId
              .first,
          31,
        );
        final pending = await repository.readEdge(
          'p',
          const EdgeRef('tail', 'head'),
          policy: OfflineReadPolicy.cacheOnly,
        );
        expect(pending.state, OfflineReadState.unknown);
        expect(pending.hasPendingWrites, isTrue);

        expect(await repository.drain('p'), 2);
        final status = await repository.getWriteStatus('p', 'add-edges');
        expect(
          status!.items.map(
            (item) =>
                (item.receiptResult as OfflineEdgeAddReceiptResult)
                    .effectiveWeight,
          ),
          <double>[2, double.infinity],
        );
        expect(
          remote.receiptSendContexts.every(
            (context) =>
                context.mutation == ReceiptMutationKind.edgeAdd &&
                context.operationIds.length == 1,
          ),
          isTrue,
        );
      },
    );

    test('receipt Add validates contribution IDs before capability I/O', () async {
      final clock = MutableClock(initial);
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);

      for (final contributionId in <Uint8List?>[
        null,
        Uint8List(24),
        testBytes(23, 1),
        testBytes(25, 1),
      ]) {
        await expectLater(
          repository.addEdge(
            partitionId: 'p',
            input: EdgeInput(
              tail: 'tail',
              head: 'head',
              weight: 1,
              contribId: contributionId,
            ),
          ),
          throwsA(isA<OfflineArgumentException>()),
        );
      }
      await expectLater(
        repository.addEdges(
          partitionId: 'p',
          inputs: <EdgeInput>[
            EdgeInput(
              tail: 'tail',
              head: 'one',
              weight: 1,
              contribId: testBytes(24, 1),
            ),
            EdgeInput(
              tail: 'tail',
              head: 'two',
              weight: 1,
              contribId: testBytes(24, 1),
            ),
          ],
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(remote.receiptPrepareCalls, 0);
    });

    test('born-expired receipt Add retains exact zero after restart', () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote()
        ..receiptSendResults.add(OfflineEdgeAddReceiptResult(0));
      final config = testConfig(clock);
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: config,
      );
      final handle = await repository.addEdge(
        partitionId: 'p',
        input: EdgeInput(
          tail: 'expired',
          head: 'edge',
          weight: 7,
          expiresAt: initial.subtract(const Duration(microseconds: 1)),
          contribId: testBytes(24, 7),
        ),
      );
      await repository.dispose();

      final restored = OfflineLanternRepository(
        store: InMemoryOfflineStore.fromSnapshot(await store.exportSnapshot()),
        remote: remote,
        config: config,
      );
      addTearDown(restored.dispose);
      expect(await restored.drain('p'), 1);
      final status = await restored.getWriteStatus('p', handle.operationId);
      expect(status!.items.single.state, OfflineWriteState.confirmed);
      expect(
        (status.items.single.receiptResult as OfflineEdgeAddReceiptResult)
            .effectiveWeight,
        0,
      );
    });

    test('born-expired receipt Put retains the exact server outcome', () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote()
        ..receiptSendResults.add(
          const OfflineVertexPutReceiptResult(PutOutcome.expired),
        );
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );

      final handle = await repository.putVertexIfAbsent(
        partitionId: 'p',
        input: VertexInput(
          key: 'expired',
          value: VertexValue.string('value'),
          expiresAt: initial.subtract(const Duration(microseconds: 1)),
        ),
      );

      final persisted = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      expect(persisted.state, OfflineOutboxState.enqueued);
      expect(persisted.intent.expiration!.isBefore(initial), isTrue);
      final pending = await repository.getWriteStatus('p', handle.operationId);
      expect(pending!.items.single.state, OfflineWriteState.locallyCommitted);
      expect(pending.items.single.receiptResult, isNull);
      await repository.dispose();

      final restoredStore = InMemoryOfflineStore.fromSnapshot(
        await store.exportSnapshot(),
      );
      final restored = OfflineLanternRepository(
        store: restoredStore,
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(restored.dispose);
      expect(
        (await restoredStore.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        )).state,
        OfflineOutboxState.enqueued,
      );
      expect(await restored.drain('p'), 1);
      final status = await restored.getWriteStatus('p', handle.operationId);
      expect(status!.items.single.state, OfflineWriteState.confirmed);
      expect(
        (status.items.single.receiptResult as OfflineVertexPutReceiptResult)
            .outcome,
        PutOutcome.expired,
      );

      final boundedStore = InMemoryOfflineStore();
      final bounded = OfflineLanternRepository(
        store: boundedStore,
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxWriteStatusControllers: 1,
        ),
      );
      addTearDown(bounded.dispose);
      await expectLater(
        bounded.putVerticesIfAbsent(
          partitionId: 'bounded',
          inputs: <VertexInput>[
            VertexInput(
              key: 'first',
              value: VertexValue.nil(),
              expiresAt: initial.subtract(const Duration(microseconds: 1)),
            ),
            VertexInput(
              key: 'second',
              value: VertexValue.nil(),
              expiresAt: initial.subtract(const Duration(microseconds: 1)),
            ),
          ],
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        await boundedStore.transaction(
          (transaction) async => await transaction.outbox('bounded'),
        ),
        isEmpty,
      );
    });

    test('lost response is reconciled after restart without resend', () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote()
        ..receiptSendResults.add(const OfflineEdgeDeleteReceiptResult(false))
        ..receiptSendFailures.add(
          failure(OfflineRemoteErrorKind.outcomeUnknown),
        )
        ..commitBeforeReceiptSendFailure = true;
      final config = OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (ceiling) => ceiling,
        baseRetryDelay: const Duration(microseconds: 1),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: config,
      );

      final handle = await repository.deleteEdge(
        partitionId: 'p',
        edge: const EdgeRef('tail', 'head'),
        operationId: 'delete-edge',
      );
      final beforeSend = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      expect(await repository.drain('p'), 0);
      expect(remote.receiptSendCalls, 1);
      expect(
        remote.receiptSendContexts.single.operationIds.single,
        beforeSend.receipt!.operationId,
      );
      await repository.dispose();

      final restoredStore = InMemoryOfflineStore.fromSnapshot(
        await store.exportSnapshot(),
      );
      final restored = OfflineLanternRepository(
        store: restoredStore,
        remote: remote,
        config: config,
      );
      addTearDown(restored.dispose);
      clock.advance(const Duration(microseconds: 1));
      remote.receiptCalls.clear();

      expect(await restored.drain('p'), 1);
      expect(remote.receiptCalls, <String>['status']);
      expect(remote.receiptSendCalls, 1);
      final status = await restored.getWriteStatus('p', handle.operationId);
      expect(status!.items.single.state, OfflineWriteState.confirmed);
      expect(
        (status.items.single.receiptResult as OfflineEdgeDeleteReceiptResult)
            .existed,
        isFalse,
      );
    });

    for (final delay in <Duration>[
      const Duration(minutes: 6),
      const Duration(hours: 25),
    ]) {
      test(
        'proven-unsent Add refreshes after $delay without changing its intent',
        () async {
          final clock = MutableClock(initial);
          final store = InMemoryOfflineStore();
          final remote = FakeOfflineRemote()
            ..receiptCapability = offlineReceiptCapability(
              serverNow: clock.now,
            )
            ..receiptSendResults.add(const OfflineEdgeAddReceiptResult(4));
          final repository = OfflineLanternRepository(
            store: store,
            remote: remote,
            config: testConfig(clock),
          );
          addTearDown(repository.dispose);
          final expiration = initial.add(const Duration(days: 3));
          final contributionId = testBytes(24, 41);
          final handle = await repository.addEdge(
            partitionId: 'p',
            operationId: 'add-operation',
            input: EdgeInput(
              tail: 'tail',
              head: 'head',
              weight: 3,
              expiresAt: expiration,
              contribId: contributionId,
            ),
          );
          final queued = await store.transaction(
            (transaction) async => (await transaction.outbox('p')).single,
          );
          expect(queued.receipt!.mayHaveDispatched, isFalse);

          clock.advance(delay);
          remote.receiptCapability = offlineReceiptCapability(
            serverNow: clock.now,
          );
          if (delay > const Duration(hours: 24)) {
            remote.receiptStatuses[queued.receipt!.operationId] =
                OfflineReceiptStatus(
                  operationId: queued.receipt!.operationId,
                  state: ReceiptStatusState.noLongerProvable,
                );
          }
          remote.beforeReceiptSend = (context) async {
            final persisted = await store.transaction(
              (transaction) async => (await transaction.outbox('p')).single,
            );
            expect(persisted.receipt!.mayHaveDispatched, isTrue);
            expect(persisted.receipt!.operationId, context.operationIds.single);
            expect(persisted.receipt!.groupId, context.groupId);
            expect(persisted.recordId, queued.recordId);
            expect(persisted.operationId, queued.operationId);
            expect(persisted.enqueuedAt, queued.enqueuedAt);
            expect(persisted.attemptCount, 0);
            final intent = persisted.intent as OfflineReceiptAddEdgeIntent;
            expect(intent.contributionId, contributionId);
            expect(intent.edge.expiration, expiration);
            expect(intent.edge.weight, 3);
          };
          remote.receiptCalls.clear();

          expect(await repository.drain('p'), 1);
          expect(remote.receiptCalls, <String>[
            'capability',
            'prepare',
            'status',
            'capability',
            'send',
          ]);
          expect(remote.receiptStatusIds, <ReceiptOperationId>[
            remote.receiptSendContexts.single.operationIds.single,
          ]);
          expect(
            remote.receiptSendContexts.single.operationIds.single,
            isNot(queued.receipt!.operationId),
          );
          expect(
            remote.receiptSendContexts.single.groupId,
            isNot(queued.receipt!.groupId),
          );
          final status = await repository.getWriteStatus(
            'p',
            handle.operationId,
          );
          expect(status!.items.single.recordId, queued.recordId);
          expect(status.items.single.attemptCount, 1);
          expect(
            (status.items.single.receiptResult as OfflineEdgeAddReceiptResult)
                .effectiveWeight,
            4,
          );
        },
      );
    }

    test('status latency cannot send a newly stale unsent ID', () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote()
        ..receiptCapability = offlineReceiptCapability(serverNow: clock.now);
      var advanced = false;
      remote.beforeReceiptStatusReturn = (_) async {
        if (!advanced) {
          advanced = true;
          clock.advance(const Duration(minutes: 6));
          remote.receiptCapability = offlineReceiptCapability(
            serverNow: clock.now,
          );
        }
      };
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          leaseDuration: const Duration(hours: 1),
        ),
      );
      addTearDown(repository.dispose);
      final handle = await repository.deleteEdge(
        partitionId: 'p',
        edge: const EdgeRef('tail', 'head'),
      );
      final original = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      remote.receiptCalls.clear();

      expect(await repository.drain('p'), 1);
      expect(remote.receiptStatusIds, hasLength(2));
      expect(remote.receiptStatusIds.first, original.receipt!.operationId);
      expect(
        remote.receiptStatusIds.last,
        remote.receiptSendContexts.single.operationIds.single,
      );
      expect(
        remote.receiptStatusIds.last,
        isNot(original.receipt!.operationId),
      );
      expect(remote.receiptSendCalls, 1);
      expect(
        (await repository.getWriteStatus(
          'p',
          handle.operationId,
        ))!.items.single.attemptCount,
        1,
      );
    });

    test('crash before the send marker preserves unsent proof', () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final entered = Completer<void>();
      final release = Completer<void>();
      final remote = FakeOfflineRemote()
        ..receiptCapability = offlineReceiptCapability(serverNow: clock.now)
        ..beforeReceiptStatusReturn = (_) async {
          entered.complete();
          await release.future;
        };
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      final handle = await repository.deleteEdge(
        partitionId: 'p',
        edge: const EdgeRef('tail', 'head'),
      );
      final queued = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      clock.advance(const Duration(minutes: 6));
      remote.receiptCapability = offlineReceiptCapability(
        serverNow: clock.now,
      );
      final draining = repository.drain('p');
      late final String snapshot;
      late final OfflineOutboxRecord rekeyed;
      try {
        await entered.future;
        rekeyed = await store.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        );
        expect(rekeyed.receipt!.mayHaveDispatched, isFalse);
        expect(
          rekeyed.receipt!.operationId,
          isNot(queued.receipt!.operationId),
        );
        expect(remote.receiptSendCalls, 0);
        snapshot = await store.exportSnapshot();
      } finally {
        release.complete();
      }
      expect(await draining, 1);
      await repository.dispose();

      clock.advance(const Duration(minutes: 6));
      final restoredRemote = FakeOfflineRemote()
        ..receiptCapability = offlineReceiptCapability(serverNow: clock.now);
      final restoredStore = InMemoryOfflineStore.fromSnapshot(snapshot);
      final restored = OfflineLanternRepository(
        store: restoredStore,
        remote: restoredRemote,
        config: testConfig(clock),
      );
      addTearDown(restored.dispose);

      expect(await restored.drain('p'), 1);
      expect(restoredRemote.receiptStatusIds, hasLength(1));
      expect(
        restoredRemote.receiptStatusIds.single,
        restoredRemote.receiptSendContexts.single.operationIds.single,
      );
      expect(
        restoredRemote.receiptStatusIds.single,
        isNot(rekeyed.receipt!.operationId),
      );
      expect(restoredRemote.receiptSendCalls, 1);
      expect(
        (await restored.getWriteStatus(
          'p',
          handle.operationId,
        ))!.items.single.attemptCount,
        1,
      );
    });

    for (final scenario in <String>[
      'disabled',
      'changed-node',
      'regressed-clock',
    ]) {
      test('receipt refresh $scenario fails closed before status', () async {
        final clock = MutableClock(initial);
        final store = InMemoryOfflineStore();
        final remote = FakeOfflineRemote()
          ..receiptCapability = offlineReceiptCapability(
            serverNow: clock.now,
          );
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: testConfig(clock),
        );
        addTearDown(repository.dispose);
        final handle = await repository.deleteVertex(
          partitionId: 'p',
          key: 'target',
        );
        final original = await store.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        );
        clock.advance(const Duration(minutes: 6));
        remote.receiptCapability = offlineReceiptCapability(
          serverNow: clock.now,
        );
        remote.beforeReceiptPrepare = () async {
          remote.receiptCapability = switch (scenario) {
            'disabled' => const OfflineReceiptCapabilityDisabled(),
            'changed-node' => offlineReceiptCapability(
              node: 99,
              serverNow: clock.now,
            ),
            _ => offlineReceiptCapability(serverNow: initial),
          };
        };
        remote.receiptCalls.clear();

        expect(await repository.drain('p'), 0);
        expect(remote.receiptCalls, <String>['capability', 'prepare']);
        expect(remote.receiptSendCalls, 0);
        final record = await store.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        );
        expect(record.receipt!.operationId, original.receipt!.operationId);
        expect(record.receipt!.mayHaveDispatched, isFalse);
        final status = await repository.getWriteStatus(
          'p',
          handle.operationId,
        );
        expect(status!.items.single.state, OfflineWriteState.outcomeUnknown);
        expect(
          status.items.single.diagnosticCode,
          scenario == 'disabled'
              ? 'receipt_capability_disabled'
              : scenario == 'changed-node'
              ? 'receipt_continuity_changed'
              : 'receipt_protocol',
        );
      });
    }

    test('crash after send with zero counted attempts never rekeys', () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final sent = Completer<void>();
      final release = Completer<void>();
      final remote = FakeOfflineRemote()
        ..afterReceiptSend = (_) async {
          sent.complete();
          await release.future;
        };
      final config = testConfig(clock);
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: config,
      );
      final handle = await repository.deleteVertex(
        partitionId: 'p',
        key: 'target',
      );
      final initialRecord = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );

      final draining = repository.drain('p');
      late final String snapshot;
      try {
        await sent.future;
        final inFlight = await store.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        );
        expect(inFlight.state, OfflineOutboxState.sending);
        expect(inFlight.attemptCount, 0);
        expect(inFlight.receipt!.mayHaveDispatched, isTrue);
        expect(
          inFlight.receipt!.operationId,
          initialRecord.receipt!.operationId,
        );
        snapshot = await store.exportSnapshot();
      } finally {
        release.complete();
      }
      expect(await draining, 1);
      await repository.dispose();

      clock.advance(const Duration(minutes: 6));
      remote.receiptCapability = offlineReceiptCapability(
        serverNow: clock.now,
      );
      remote.receiptCalls.clear();
      final restored = OfflineLanternRepository(
        store: InMemoryOfflineStore.fromSnapshot(snapshot),
        remote: remote,
        config: config,
      );
      addTearDown(restored.dispose);

      expect(await restored.drain('p'), 1);
      expect(remote.receiptCalls, <String>['status']);
      expect(remote.receiptSendCalls, 1);
      expect(remote.receiptPrepareCalls, 1);
      expect(remote.receiptStatusIds.last, initialRecord.receipt!.operationId);
      final status = await restored.getWriteStatus('p', handle.operationId);
      expect(status!.items.single.attemptCount, 0);
      expect(
        (status.items.single.receiptResult as OfflineVertexDeleteReceiptResult)
            .existed,
        isFalse,
      );
    });

    test('crash after marker but before send cannot refresh', () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final entered = Completer<void>();
      final release = Completer<void>();
      final original = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote()
          ..beforeReceiptSend = (_) async {
            entered.complete();
            await release.future;
          },
        config: testConfig(clock),
      );
      final handle = await original.deleteEdge(
        partitionId: 'p',
        edge: const EdgeRef('tail', 'head'),
      );
      final draining = original.drain('p');
      late final String snapshot;
      late final OfflineOutboxRecord marked;
      try {
        await entered.future;
        marked = await store.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        );
        expect(marked.receipt!.mayHaveDispatched, isTrue);
        expect(marked.attemptCount, 0);
        snapshot = await store.exportSnapshot();
      } finally {
        release.complete();
      }
      expect(await draining, 1);
      await original.dispose();

      clock.advance(const Duration(hours: 25));
      final remote = FakeOfflineRemote()
        ..receiptCapability = offlineReceiptCapability(serverNow: clock.now);
      remote.receiptStatuses[marked.receipt!.operationId] =
          OfflineReceiptStatus(
            operationId: marked.receipt!.operationId,
            state: ReceiptStatusState.noLongerProvable,
          );
      final restoredStore = InMemoryOfflineStore.fromSnapshot(snapshot);
      final restored = OfflineLanternRepository(
        store: restoredStore,
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(restored.dispose);

      expect(await restored.drain('p'), 0);
      expect(remote.receiptCalls, <String>['status']);
      expect(remote.receiptSendCalls, 0);
      expect(remote.receiptPrepareCalls, 0);
      final unresolved = await restored.getWriteStatus(
        'p',
        handle.operationId,
      );
      expect(unresolved!.items.single.state, OfflineWriteState.outcomeUnknown);
      expect(unresolved.items.single.attemptCount, 0);
      expect(
        (await restoredStore.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        )).receipt!.operationId,
        marked.receipt!.operationId,
      );
    });

    test('missing dispatch marker is not proof of an unsent ID', () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      final handle = await repository.deleteVertex(
        partitionId: 'p',
        key: 'target',
      );
      final queued = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      final snapshot =
          jsonDecode(await store.exportSnapshot()) as Map<String, Object?>;
      final partition =
          (snapshot['partitions']! as List<Object?>).single!
              as Map<String, Object?>;
      final outbox = partition['outbox']! as List<Object?>;
      final record = jsonDecode(outbox.single! as String)
          as Map<String, Object?>;
      record['schema'] = 3;
      (record['receipt']! as Map<String, Object?>).remove(
        'mayHaveDispatched',
      );
      outbox[0] = jsonEncode(record);
      await repository.dispose();

      clock.advance(const Duration(hours: 25));
      remote.receiptCapability = offlineReceiptCapability(
        serverNow: clock.now,
      );
      remote.receiptStatuses[queued.receipt!.operationId] =
          OfflineReceiptStatus(
            operationId: queued.receipt!.operationId,
            state: ReceiptStatusState.noLongerProvable,
          );
      final restoredStore = InMemoryOfflineStore.fromSnapshot(
        jsonEncode(snapshot),
      );
      final migrated = await restoredStore.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      expect(migrated.receipt!.mayHaveDispatched, isTrue);
      expect(migrated.attemptCount, 0);
      final restored = OfflineLanternRepository(
        store: restoredStore,
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(restored.dispose);

      expect(await restored.drain('p'), 0);
      expect(remote.receiptCalls, <String>['prepare', 'status']);
      expect(remote.receiptSendCalls, 0);
      expect(
        (await restoredStore.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        )).receipt!.operationId,
        queued.receipt!.operationId,
      );
      final unresolved = await restored.getWriteStatus(
        'p',
        handle.operationId,
      );
      expect(unresolved!.items.single.state, OfflineWriteState.outcomeUnknown);
      expect(unresolved.items.single.attemptCount, 0);
    });

    test(
      'lost Add response confirms semantic NaN after restart without resend',
      () async {
        final clock = MutableClock(initial);
        final store = InMemoryOfflineStore();
        final remote = FakeOfflineRemote()
          ..receiptSendResults.add(OfflineEdgeAddReceiptResult(double.nan))
          ..receiptSendFailures.add(
            failure(OfflineRemoteErrorKind.outcomeUnknown),
          )
          ..commitBeforeReceiptSendFailure = true;
        final config = OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (ceiling) => ceiling,
          baseRetryDelay: const Duration(microseconds: 1),
        );
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: config,
        );
        final contributionId = testBytes(24, 41);
        final handle = await repository.addEdge(
          partitionId: 'p',
          operationId: 'add-edge',
          input: EdgeInput(
            tail: 'tail',
            head: 'head',
            weight: 5,
            contribId: contributionId,
          ),
        );
        final beforeSend = await store.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        );
        expect(
          (beforeSend.intent as OfflineReceiptAddEdgeIntent).contributionId,
          contributionId,
        );
        expect(await repository.drain('p'), 0);
        expect(remote.receiptSendCalls, 1);
        expect(
          (await store.transaction(
            (transaction) async => (await transaction.outbox('p')).single,
          )).attemptCount,
          1,
        );
        await repository.dispose();

        final restoredStore = InMemoryOfflineStore.fromSnapshot(
          await store.exportSnapshot(),
        );
        final restored = OfflineLanternRepository(
          store: restoredStore,
          remote: remote,
          config: config,
        );
        addTearDown(restored.dispose);
        clock.advance(const Duration(microseconds: 1));
        remote.receiptCalls.clear();

        expect(await restored.drain('p'), 1);
        expect(remote.receiptCalls, <String>['status']);
        expect(remote.receiptSendCalls, 1);
        final status = await restored.getWriteStatus('p', handle.operationId);
        expect(status!.items.single.attemptCount, 1);
        expect(
          (status.items.single.receiptResult as OfflineEdgeAddReceiptResult)
              .effectiveWeight
              .isNaN,
          isTrue,
        );
      },
    );

    test('exhausted mutation attempts remain outcome unknown', () async {
      final clock = MutableClock(initial);
      final remote = FakeOfflineRemote()
        ..receiptSendFailures.add(
          failure(OfflineRemoteErrorKind.outcomeUnknown),
        );
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxAttempts: 1,
        ),
      );
      addTearDown(repository.dispose);
      final handle = await repository.deleteEdge(
        partitionId: 'p',
        edge: const EdgeRef('tail', 'head'),
      );

      expect(await repository.drain('p'), 0);
      expect(remote.receiptSendCalls, 1);
      clock.advance(const Duration(microseconds: 1));
      expect(await repository.drain('p'), 0);
      expect(remote.receiptSendCalls, 1);
      final status = await repository.getWriteStatus('p', handle.operationId);
      expect(status!.items.single.state, OfflineWriteState.outcomeUnknown);
      expect(status.items.single.diagnosticCode, 'receipt_attempts_exhausted');
    });

    test(
      'lookup uncertainty stays unresolved and status precedes resend',
      () async {
        final clock = MutableClock(initial);
        final store = InMemoryOfflineStore(
          limits: const OfflineStoreLimits(
            maxDiagnosticCodeBytes: offlineMinimumDiagnosticCodeBytes,
          ),
        );
        final remote = FakeOfflineRemote()
          ..receiptStatusFailures.add(
            failure(OfflineRemoteErrorKind.resourceExhausted),
          )
          ..receiptSendResults.add(
            const OfflineVertexDeleteReceiptResult(false),
          );
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: OfflineConfig(
            clock: clock.call,
            idGenerator: testConfig(clock).idGenerator,
            jitter: (ceiling) => ceiling,
            baseRetryDelay: const Duration(microseconds: 1),
          ),
        );
        addTearDown(repository.dispose);
        final handle = await repository.deleteVertex(
          partitionId: 'p',
          key: 'missing',
          operationId: 'delete-vertex',
        );
        remote.receiptCalls.clear();

        expect(await repository.drain('p'), 0);
        expect(remote.receiptCalls, <String>['capability', 'status']);
        expect(remote.receiptSendCalls, 0);
        final unresolved = await store.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        );
        expect(
          unresolved.receipt!.state,
          OfflineReceiptReconciliationState.lookupUnknown,
        );
        expect(
          (await repository.getWriteStatus(
            'p',
            handle.operationId,
          ))!.items.single.state,
          OfflineWriteState.retryScheduled,
        );

        clock.advance(const Duration(microseconds: 1));
        remote.receiptCalls.clear();
        expect(await repository.drain('p'), 1);
        expect(remote.receiptCalls, <String>[
          'capability',
          'status',
          'capability',
          'send',
        ]);
        final confirmed = await repository.getWriteStatus(
          'p',
          handle.operationId,
        );
        expect(
          (confirmed!.items.single.receiptResult
                  as OfflineVertexDeleteReceiptResult)
              .existed,
          isFalse,
        );
      },
    );

    test('lost proof and changed continuity or support never resend', () async {
      for (final scenario in <String>[
        'no-longer-provable',
        'disabled',
        'mutation',
        'node',
        'generation',
        'epoch',
        'retention',
        'max-entries',
        'max-bytes',
        'fingerprint',
      ]) {
        final clock = MutableClock(initial);
        final store = InMemoryOfflineStore();
        final remote = FakeOfflineRemote();
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: testConfig(clock),
        );
        addTearDown(repository.dispose);
        remote.edges[const EdgeRef('tail', 'head')] = Edge(
          tail: 'tail',
          head: 'head',
          weight: 1,
          expiration: null,
        );
        expect(
          (await repository.readEdge(
            scenario,
            const EdgeRef('tail', 'head'),
            policy: OfflineReadPolicy.serverOnly,
          )).state,
          OfflineReadState.fresh,
        );
        final handle = await repository.deleteEdge(
          partitionId: scenario,
          edge: const EdgeRef('tail', 'head'),
          operationId: 'delete',
        );
        final record = await store.transaction(
          (transaction) async => (await transaction.outbox(scenario)).single,
        );
        if (scenario == 'no-longer-provable') {
          remote.receiptStatuses[record.receipt!.operationId] =
              OfflineReceiptStatus(
                operationId: record.receipt!.operationId,
                state: ReceiptStatusState.noLongerProvable,
              );
        } else {
          remote.receiptCapability = switch (scenario) {
            'disabled' => const OfflineReceiptCapabilityDisabled(),
            'mutation' => offlineReceiptCapability(
              supportedMutations: const <ReceiptMutationKind>{
                ReceiptMutationKind.vertexPut,
                ReceiptMutationKind.vertexDelete,
              },
            ),
            'node' => offlineReceiptCapability(node: 99),
            'generation' => offlineReceiptCapability(generation: 99),
            'epoch' => offlineReceiptCapability(epoch: 99),
            'retention' => offlineReceiptCapability(
              retention: const Duration(hours: 23),
            ),
            'max-entries' => offlineReceiptCapability(maxEntries: 2048),
            'max-bytes' => offlineReceiptCapability(maxBytes: 2 * 1024 * 1024),
            'fingerprint' => offlineReceiptCapability(fingerprint: 99),
            _ => throw StateError('unknown continuity scenario'),
          };
        }
        remote.receiptCalls.clear();

        expect(await repository.drain(scenario), 0);
        expect(remote.receiptSendCalls, 0);
        expect(
          remote.receiptCalls,
          scenario == 'no-longer-provable'
              ? <String>['capability', 'status']
              : <String>['capability'],
        );
        final status = await repository.getWriteStatus(
          scenario,
          handle.operationId,
        );
        expect(status!.items.single.state, OfflineWriteState.outcomeUnknown);
        expect(status.outcomeUnknownCount, 1);
        expect(status.items.single.diagnosticCode, switch (scenario) {
          'no-longer-provable' => 'receipt_no_longer_provable',
          'disabled' => 'receipt_capability_disabled',
          'mutation' => 'receipt_mutation_unavailable',
          _ => 'receipt_continuity_changed',
        });
        expect(
          (await repository.readEdge(
            scenario,
            const EdgeRef('tail', 'head'),
            policy: OfflineReadPolicy.cacheOnly,
          )).state,
          OfflineReadState.unknown,
        );
        await expectLater(
          repository.retryDeadLetter(scenario, handle.recordId),
          throwsA(isA<OfflineUnsupportedOperationException>()),
        );
      }
    });

    test('auth rotation resumes status-first with the exact context', () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote()
        ..receiptStatusFailures.add(
          failure(OfflineRemoteErrorKind.unauthenticated),
        );
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final handle = await repository.putVertexIfAbsent(
        partitionId: 'p',
        input: VertexInput(key: 'v', value: VertexValue.string('value')),
      );
      final receipt = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single.receipt!,
      );

      expect(await repository.drain('p'), 0);
      expect(await repository.isReplayPausedForAuth('p'), isTrue);
      expect(remote.receiptSendCalls, 0);
      remote.receiptCalls.clear();
      expect(await repository.resume('p'), 1);
      expect(remote.receiptCalls, <String>[
        'capability',
        'status',
        'capability',
        'send',
      ]);
      expect(
        remote.receiptSendContexts.single.operationIds.single,
        receipt.operationId,
      );
      expect(
        (await repository.getWriteStatus(
          'p',
          handle.operationId,
        ))!.items.single.state,
        OfflineWriteState.confirmed,
      );
    });

    test(
      'receipt max age becomes unknown and invalidates cached state',
      () async {
        final clock = MutableClock(initial);
        final remote = FakeOfflineRemote()
          ..vertices['v'] = Vertex(
            key: 'v',
            value: VertexValue.string('cached'),
            expiration: null,
          );
        final repository = OfflineLanternRepository(
          store: InMemoryOfflineStore(),
          remote: remote,
          config: OfflineConfig(
            clock: clock.call,
            idGenerator: testConfig(clock).idGenerator,
            jitter: (_) => Duration.zero,
            maxAge: const Duration(seconds: 1),
          ),
        );
        addTearDown(repository.dispose);
        await repository.readVertex(
          'p',
          'v',
          policy: OfflineReadPolicy.serverOnly,
        );
        final handle = await repository.deleteVertex(
          partitionId: 'p',
          key: 'v',
        );
        clock.advance(const Duration(seconds: 1));

        final status = await repository.getWriteStatus('p', handle.operationId);
        expect(status!.items.single.state, OfflineWriteState.outcomeUnknown);
        expect(status.items.single.diagnosticCode, 'receipt_max_age');
        expect(remote.receiptStatusCalls, 0);
        expect(
          (await repository.readVertex(
            'p',
            'v',
            policy: OfflineReadPolicy.cacheOnly,
          )).state,
          OfflineReadState.unknown,
        );
      },
    );

    test('max age crossing capability never dispatches a mutation', () async {
      final clock = MutableClock(initial);
      final capabilityStarted = Completer<void>();
      final releaseCapability = Completer<void>();
      final remote = FakeOfflineRemote()
        ..beforeReceiptCapabilityReturn = () async {
          if (!capabilityStarted.isCompleted) {
            capabilityStarted.complete();
            await releaseCapability.future;
          }
        };
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxAge: const Duration(seconds: 1),
        ),
      );
      addTearDown(repository.dispose);
      final handle = await repository.deleteEdge(
        partitionId: 'p',
        edge: const EdgeRef('tail', 'head'),
      );

      final drain = repository.drain('p');
      await capabilityStarted.future;
      clock.advance(const Duration(seconds: 1));
      final duringCapability = await repository.getWriteStatus(
        'p',
        handle.operationId,
      );
      expect(duringCapability!.items.single.state, OfflineWriteState.sending);
      releaseCapability.complete();

      expect(await drain, 0);
      expect(remote.receiptCalls, <String>[
        'prepare',
        'capability',
        'status',
        'capability',
      ]);
      expect(remote.receiptSendCalls, 0);
      final terminal = await repository.getWriteStatus('p', handle.operationId);
      expect(terminal!.items.single.state, OfflineWriteState.outcomeUnknown);
      expect(terminal.items.single.diagnosticCode, 'receipt_max_age');
    });

    test('capability failures never create durable work', () async {
      for (final disabled in <bool>[true, false]) {
        final clock = MutableClock(initial);
        final store = InMemoryOfflineStore();
        final remote = FakeOfflineRemote();
        if (disabled) {
          remote.receiptCapability = const OfflineReceiptCapabilityDisabled();
        } else {
          remote.receiptPrepareFailures.add(
            failure(OfflineRemoteErrorKind.unavailable),
          );
        }
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: testConfig(clock),
        );
        addTearDown(repository.dispose);

        await expectLater(
          repository.deleteVertex(partitionId: 'p', key: 'v'),
          throwsA(
            disabled
                ? isA<OfflineReceiptCapabilityException>()
                : isA<OfflineRemoteFailure>(),
          ),
        );
        expect(
          await store.transaction(
            (transaction) async => await transaction.outbox('p'),
          ),
          isEmpty,
        );
        expect(
          await store.transaction(
            (transaction) async => await transaction.operations('p'),
          ),
          isEmpty,
        );
      }

      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote()
        ..receiptCapability = offlineReceiptCapability(
          supportedMutations: const <ReceiptMutationKind>{
            ReceiptMutationKind.edgeDelete,
          },
        );
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      try {
        await repository.deleteVertex(partitionId: 'p', key: 'v');
        fail('unsupported receipt family was enqueued');
      } on OfflineReceiptCapabilityException catch (error) {
        expect(
          error.failure,
          OfflineReceiptCapabilityFailure.mutationUnavailable,
        );
      }
      expect(
        await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        ),
        isEmpty,
      );
    });

    test('invalid caller identity is rejected before capability I/O', () async {
      final clock = MutableClock(initial);
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);

      await expectLater(
        repository.deleteVertex(partitionId: 'p', key: 'v', operationId: ''),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(remote.receiptPrepareCalls, 0);
    });

    test('malformed confirmed status fails closed without mutation', () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final handle = await repository.deleteVertex(partitionId: 'p', key: 'v');
      final record = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      remote.receiptStatuses[record.receipt!.operationId] =
          OfflineReceiptStatus(
            operationId: record.receipt!.operationId,
            state: ReceiptStatusState.confirmed,
            groupId: ReceiptGroupId(testBytes(16, 99)),
            mutation: ReceiptMutationKind.vertexDelete,
            itemIndex: 0,
            itemCount: 1,
            result: const OfflineVertexDeleteReceiptResult(true),
          );

      expect(await repository.drain('p'), 0);
      expect(remote.receiptSendCalls, 0);
      final status = await repository.getWriteStatus('p', handle.operationId);
      expect(status!.items.single.state, OfflineWriteState.outcomeUnknown);
      expect(status.items.single.diagnosticCode, 'receipt_protocol');
    });

    test('malformed mutation response records the attempted send', () async {
      final clock = MutableClock(initial);
      final remote = FakeOfflineRemote()
        ..receiptSendResults.add(const OfflineEdgeDeleteReceiptResult(true));
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final handle = await repository.deleteVertex(partitionId: 'p', key: 'v');

      expect(await repository.drain('p'), 0);
      expect(remote.receiptSendCalls, 1);
      final status = await repository.getWriteStatus('p', handle.operationId);
      expect(status!.items.single.state, OfflineWriteState.outcomeUnknown);
      expect(status.items.single.attemptCount, 1);
      expect(status.items.single.diagnosticCode, 'receipt_protocol');
    });

    test('incomplete confirmed status fails with a typed error', () {
      final capability = offlineReceiptCapability();
      final operationId = testReceiptOperationId(
        epoch: capability.policy.deploymentEpoch,
        random: 1,
      );

      expect(
        () => OfflineReceiptStatus(
          operationId: operationId,
          state: ReceiptStatusState.confirmed,
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(
        () => OfflineReceiptStatus(
          operationId: operationId,
          state: ReceiptStatusState.confirmed,
          groupId: ReceiptGroupId(testBytes(16, 21)),
          mutation: ReceiptMutationKind.vertexDelete,
          itemIndex: 0,
          itemCount: 1,
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
    });

    test(
      'non-confirmed status must match the requested operation ID',
      () async {
        final clock = MutableClock(initial);
        final store = InMemoryOfflineStore();
        final remote = FakeOfflineRemote();
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: testConfig(clock),
        );
        addTearDown(repository.dispose);
        final handle = await repository.deleteVertex(
          partitionId: 'p',
          key: 'v',
        );
        final record = await store.transaction(
          (transaction) async => (await transaction.outbox('p')).single,
        );
        remote.receiptStatuses[record.receipt!.operationId] =
            OfflineReceiptStatus(
              operationId: testReceiptOperationId(
                epoch: record.receipt!.policy.deploymentEpoch,
                random: 99,
              ),
              state: ReceiptStatusState.notYetObserved,
            );

        expect(await repository.drain('p'), 0);
        expect(remote.receiptCapabilityCalls, 1);
        expect(remote.receiptSendCalls, 0);
        final status = await repository.getWriteStatus('p', handle.operationId);
        expect(status!.items.single.state, OfflineWriteState.outcomeUnknown);
        expect(status.items.single.diagnosticCode, 'receipt_protocol');
      },
    );
  });

  test(
    'legacy Add is never overlaid or sent and becomes inspectable terminal work',
    () async {
      final clock = MutableClock(initial);
      final legacyAdd = OfflineOutboxRecord(
        recordId: 'legacy-add',
        operationId: 'mixed-operation',
        itemIndex: 0,
        partitionId: 'p',
        intent: OfflineAddEdgeIntent(
          Edge(
            tail: 'a',
            head: 'b',
            weight: Float32Value(0.5).value,
            expiration: initial.add(const Duration(hours: 1)),
          ),
          Uint8List.fromList(List<int>.generate(24, (index) => index + 1)),
        ),
        enqueuedAt: initial,
        ordinal: 1,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: 0,
      );
      final safePut = OfflineOutboxRecord(
        recordId: 'safe-put',
        operationId: 'mixed-operation',
        itemIndex: 1,
        partitionId: 'p',
        intent: OfflinePutVertexIntent(
          Vertex(
            key: 'safe',
            value: VertexValue.string('value'),
            expiration: null,
          ),
        ),
        enqueuedAt: initial,
        ordinal: 1,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: 0,
      );
      final store = restoreLegacySnapshot(
        schema: 3,
        outbox: <OfflineOutboxRecord>[legacyAdd, safePut],
        operations: <OfflineOperationRecord>[
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: 'mixed-operation',
            items: <OfflineWriteStatus>[
              for (final record in <OfflineOutboxRecord>[legacyAdd, safePut])
                OfflineWriteStatus(
                  recordId: record.recordId,
                  operationId: record.operationId,
                  itemIndex: record.itemIndex,
                  state: OfflineWriteState.locallyCommitted,
                  attemptCount: 0,
                ),
            ],
            updatedAt: initial,
          ),
        ],
      );
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final beforeDrain = await repository.readEdge(
        'p',
        const EdgeRef('a', 'b'),
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect(beforeDrain.value, isNull);
      expect(beforeDrain.hasPendingWrites, isFalse);

      expect(await repository.drain('p'), 1);
      expect(remote.vertexPutCalls, 1);
      expect(remote.edgePutCalls, 0);
      final status = await repository.getWriteStatus('p', 'mixed-operation');
      expect(status!.isTerminal, isTrue);
      expect(status.confirmedCount, 1);
      expect(status.deadLetterCount, 1);
      expect(status.items.first.attemptCount, 0);
      expect(status.items.first.diagnosticCode, 'unsupported_add');
      final deadLetter = (await repository.listDeadLetters('p')).single;
      expect(deadLetter.category, OfflineOperationCategory.addEdge);
      expect(deadLetter.diagnosticCode, 'unsupported_add');
      final inspected = await repository.inspectDeadLetter(
        'p',
        legacyAdd.recordId,
        authorize: (_) async => true,
      );
      expect(inspected, isA<OfflineAddEdgeIntent>());
      await expectLater(
        repository.retryDeadLetter('p', legacyAdd.recordId),
        throwsA(isA<OfflineUnsupportedOperationException>()),
      );
      expect(
        (await repository.listDeadLetters('p')).single.recordId,
        'legacy-add',
      );
    },
  );

  for (final asynchronous in [false, true]) {
    test(
      'replay retries, pauses authentication, and dead-letters invalid intents (async: $asynchronous)',
      () async {
        final clock = MutableClock(initial);
        final remote = FakeOfflineRemote();
        final reference = InMemoryOfflineStore();
        final OfflineStore store = asynchronous
            ? DelayedOfflineStore(reference)
            : reference;
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: OfflineConfig(
            clock: clock.call,
            idGenerator: testConfig(clock).idGenerator,
            jitter: (ceiling) => ceiling,
            baseRetryDelay: const Duration(seconds: 1),
          ),
        );
        await repository.putVertex(
          partitionId: 'p',
          input: VertexInput(key: 'retry', value: VertexValue.string('retry')),
        );
        remote.vertexPutFailures.add(
          failure(OfflineRemoteErrorKind.unavailable),
        );
        expect(await repository.drain('p'), 0);
        expect(remote.vertexPutCalls, 1);
        clock.advance(const Duration(seconds: 1));
        expect(await repository.drain('p'), 1);
        expect(remote.vertices['retry']!.value, isA<StringValue>());

        await repository.putVertex(
          partitionId: 'p',
          input: VertexInput(key: 'auth', value: VertexValue.string('auth')),
        );
        remote.vertexPutFailures.add(
          failure(OfflineRemoteErrorKind.unauthenticated),
        );
        expect(await repository.drain('p'), 0);
        final auth = await store.transaction(
          (transaction) async => (await transaction.outbox(
            'p',
          )).singleWhere((record) => record.intent.key.vertexKey == 'auth'),
        );
        expect(auth.attemptCount, 0);
        expect(await repository.resume('p'), 1);

        await repository.putVertex(
          partitionId: 'p',
          input: VertexInput(key: 'bad', value: VertexValue.string('bad')),
        );
        remote.vertexPutFailures.add(
          failure(OfflineRemoteErrorKind.invalidArgument),
        );
        expect(await repository.drain('p'), 0);
        final dead = await repository.listDeadLetters('p');
        expect(dead, hasLength(1));
        expect(dead.single.category, OfflineOperationCategory.putVertex);
        await expectLater(
          repository.inspectDeadLetter(
            'p',
            dead.single.recordId,
            authorize: (_) async => false,
          ),
          throwsA(isA<OfflineAuthorizationException>()),
        );
        final intent = await repository.inspectDeadLetter(
          'p',
          dead.single.recordId,
          authorize: (_) => true,
        );
        expect(intent, isA<OfflinePutVertexIntent>());
      },
    );
  }

  test(
    'auth pause is partition durable and only explicit resume clears it',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxConcurrency: 1,
          maxConcurrencyPerPartition: 1,
        ),
      );
      await repository.putVertices(
        partitionId: 'p',
        inputs: <VertexInput>[
          VertexInput(key: 'a', value: VertexValue.string('a')),
          VertexInput(key: 'b', value: VertexValue.string('b')),
        ],
      );
      remote.vertexPutFailures.add(
        failure(OfflineRemoteErrorKind.unauthenticated),
      );

      expect(await repository.drain('p'), 0);
      expect(remote.vertexPutCalls, 1);
      expect(await repository.isReplayPausedForAuth('p'), isTrue);
      for (final entryPoint in <Future<int> Function()>[
        () => repository.drain('p'),
        () => repository.start('p'),
        () => repository.probeAndDrain('p'),
      ]) {
        await expectLater(
          entryPoint(),
          throwsA(isA<OfflineAuthPausedException>()),
        );
      }
      expect(remote.vertexPutCalls, 1);
      expect(remote.probeCalls, 0);
      expect(
        (await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        )).map((record) => record.attemptCount),
        everyElement(0),
      );

      final restored = OfflineLanternRepository(
        store: InMemoryOfflineStore.fromSnapshot(await store.exportSnapshot()),
        remote: remote,
        config: testConfig(clock),
      );
      expect(await restored.isReplayPausedForAuth('p'), isTrue);
      expect(await restored.resume('p'), 2);
      expect(remote.vertexPutCalls, 3);
      expect(await restored.isReplayPausedForAuth('p'), isFalse);
    },
  );

  test('schema v3 operation auth pause requires explicit resume', () async {
    final store = InMemoryOfflineStore.fromSnapshot(
      File(
        'test/fixtures/v3_snapshot_auth_pause.json',
      ).readAsStringSync().trim(),
    );
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: OfflineConfig(
        clock: () => DateTime.fromMicrosecondsSinceEpoch(1, isUtc: true),
        idGenerator: testConfig(MutableClock(initial)).idGenerator,
        jitter: (_) => Duration.zero,
      ),
    );

    expect(await repository.isReplayPausedForAuth('legacy-user'), isTrue);
    await expectLater(
      repository.drain('legacy-user'),
      throwsA(isA<OfflineAuthPausedException>()),
    );
    expect(remote.vertexPutCalls, 0);
    final migrated = await store.transaction(
      (transaction) async => (await transaction.outbox('legacy-user')).single,
    );
    expect(migrated.diagnosticCode, 'unauthenticated');
    expect(await repository.resume('legacy-user'), 1);
    expect(remote.vertexPutCalls, 1);
    expect(await repository.isReplayPausedForAuth('legacy-user'), isFalse);
  });

  test('replay send bounds apply across partitions and entry points', () async {
    final clock = MutableClock(initial);
    final remote = _ConcurrentRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxConcurrency: 2,
        maxConcurrencyPerPartition: 1,
        maxQueuedReplaySends: 2,
        maxQueuedReplaySendsPerPartition: 1,
        maxQueuedReplaysPerPartition: 1,
      ),
    );
    for (final partition in <String>['a', 'b']) {
      await repository.putVertices(
        partitionId: partition,
        inputs: <VertexInput>[
          VertexInput(key: '$partition-1', value: VertexValue.nil()),
          VertexInput(key: '$partition-2', value: VertexValue.nil()),
        ],
      );
    }
    final first = repository.start('a');
    final second = repository.probeAndDrain('b');
    await remote.twoStarted.future;
    final queued = repository.drain('a');
    await expectLater(
      repository.resume('a'),
      throwsA(isA<OfflineCapacityException>()),
    );
    expect(remote.maxActive, 2);
    expect(remote.maxActiveByPartition.values, everyElement(1));
    remote.release();
    expect(await first + await second + await queued, 4);
    expect(remote.maxActive, 2);
    expect(remote.maxActiveByPartition.values, everyElement(1));
  });

  test('canceled queued replay cannot overtake its predecessor', () async {
    final clock = MutableClock(initial);
    final remote = _AuthGateRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxConcurrency: 1,
        maxConcurrencyPerPartition: 1,
        maxQueuedReplaysPerPartition: 1,
      ),
    );
    await repository.putVertices(
      partitionId: 'p',
      inputs: <VertexInput>[
        VertexInput(key: 'a', value: VertexValue.nil()),
        VertexInput(key: 'c', value: VertexValue.nil()),
      ],
    );
    final first = repository.drain('p');
    await remote.started.future;
    final queuedCancellation = LanternCancellationToken();
    final canceled = repository.drain('p', cancellation: queuedCancellation);
    queuedCancellation.cancel();
    await expectLater(canceled, throwsA(isA<OfflineCanceledException>()));

    final third = repository.drain('p');
    await Future<void>.delayed(Duration.zero);
    expect(remote.vertexPutCalls, 1);
    remote.releaseUnauthenticated();
    expect(await first, 0);
    await expectLater(third, throwsA(isA<OfflineAuthPausedException>()));
    expect(remote.vertexPutCalls, 1);
  });

  test(
    'old partition work never sends with credentials rotated after wipe',
    () async {
      final clock = MutableClock(initial);
      var credential = 'old-token';
      final remote = _CredentialRecordingRemote(() => credential);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxConcurrency: 1,
          maxConcurrencyPerPartition: 1,
        ),
      );
      await repository.putVertices(
        partitionId: 'old-user',
        inputs: <VertexInput>[
          VertexInput(key: 'old-a', value: VertexValue.nil()),
          VertexInput(key: 'old-b', value: VertexValue.nil()),
        ],
      );
      final draining = repository.drain('old-user');
      final canceledDrain = expectLater(
        draining,
        throwsA(isA<OfflineCanceledException>()),
      );
      await remote.started.future;
      await repository.wipePartition('old-user');
      await canceledDrain;

      credential = 'new-token';
      remote.release();
      await repository.putVertex(
        partitionId: 'new-user',
        input: VertexInput(key: 'new-a', value: VertexValue.nil()),
      );
      expect(await repository.drain('new-user'), 1);
      expect(remote.credentialsForOldKeys, everyElement('old-token'));
      expect(remote.credentialsByKey['old-b'], isNull);
      expect(remote.credentialsByKey['new-a'], 'new-token');
    },
  );

  test('repeated wipe and dispose leave the lifecycle quiesced', () async {
    final clock = MutableClock(initial);
    final remote = _DelayedRemote(Completer<void>());
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'key', value: VertexValue.nil()),
    );
    final draining = repository.drain('p');
    final canceledDrain = expectLater(
      draining,
      throwsA(isA<OfflineCanceledException>()),
    );
    await remote.started.future;
    await Future.wait<void>(<Future<void>>[
      repository.wipePartition('p'),
      repository.wipePartition('p'),
    ]);
    await canceledDrain;
    await repository.wipePartition('p');
    await Future.wait<void>(<Future<void>>[
      repository.dispose(),
      repository.dispose(),
    ]);
    expect(
      () => repository.drain('p'),
      throwsA(isA<OfflineDisposedException>()),
    );
  });

  test('failed store wipe keeps the partition closed until retry', () async {
    final clock = MutableClock(initial);
    final store = _FailNextTransactionStore();
    final repository = OfflineLanternRepository(
      store: store,
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'secret', value: VertexValue.string('secret')),
    );
    store.failNext();

    await expectLater(repository.wipePartition('p'), throwsStateError);
    expect(
      () => repository.readVertex(
        'p',
        'secret',
        policy: OfflineReadPolicy.cacheOnly,
      ),
      throwsA(isA<OfflineCanceledException>()),
    );

    await repository.wipePartition('p');
    expect(
      (await repository.readVertex(
        'p',
        'secret',
        policy: OfflineReadPolicy.cacheOnly,
      )).state,
      OfflineReadState.unknown,
    );
  });

  test(
    'operation retention preserves retryable dead-letter metadata',
    () async {
      final clock = MutableClock(initial);
      final remote = FakeOfflineRemote();
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          operationRetention: const Duration(hours: 1),
          deadLetterRetention: const Duration(days: 1),
        ),
      );
      final write = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'retryable',
          value: VertexValue.string('value'),
        ),
      );
      remote.vertexPutFailures.add(
        failure(OfflineRemoteErrorKind.invalidArgument),
      );
      expect(await repository.drain('p'), 0);
      final deadLetter = (await repository.listDeadLetters('p')).single;

      clock.advance(const Duration(hours: 2));
      expect(await repository.drain('p'), 0);
      expect(
        await repository.getWriteStatus('p', write.operationId),
        isNotNull,
      );

      await repository.retryDeadLetter('p', deadLetter.recordId);
      expect(
        (await repository.getWriteStatus(
          'p',
          write.operationId,
        ))!.items.single.state,
        OfflineWriteState.locallyCommitted,
      );
      expect(await repository.drain('p'), 1);
    },
  );

  test('dead-letter retention starts at its terminal transition', () async {
    final clock = MutableClock(initial);
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxAge: const Duration(hours: 2),
        deadLetterRetention: const Duration(hours: 1),
      ),
    );
    addTearDown(repository.dispose);
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'late-dead', value: VertexValue.string('value')),
    );
    clock.advance(const Duration(minutes: 59));
    remote.vertexPutFailures.add(
      failure(OfflineRemoteErrorKind.invalidArgument),
    );
    expect(await repository.drain('p'), 0);

    clock.advance(const Duration(minutes: 2));
    expect(await repository.listDeadLetters('p'), hasLength(1));
    clock.advance(const Duration(minutes: 59));
    expect(await repository.listDeadLetters('p'), isEmpty);
  });

  test(
    'dead-letter transition time is sampled inside its transaction',
    () async {
      final clock = MutableClock(initial);
      final store = _TransactionSignalingStore(InMemoryOfflineStore());
      final remote = _GatedFailureRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          deadLetterRetention: const Duration(seconds: 1),
        ),
      );
      addTearDown(repository.dispose);
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'blocked-failure', value: VertexValue.nil()),
      );
      final draining = repository.drain('p');
      await remote.started.future;

      final blockerStarted = Completer<void>();
      final releaseBlocker = Completer<void>();
      final blocker = store.transaction<void>((_) async {
        blockerStarted.complete();
        await releaseBlocker.future;
      });
      await blockerStarted.future;
      final failureTransactionQueued = store.signalNextTransaction();
      remote.release.complete();
      await failureTransactionQueued;
      clock.advance(const Duration(seconds: 2));
      releaseBlocker.complete();
      await blocker;

      expect(await draining, 0);
      final retained = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      expect(retained.state, OfflineOutboxState.deadLetter);
      expect(retained.deadLetteredAt, clock.now);
      expect(await repository.listDeadLetters('p'), hasLength(1));
    },
  );

  test('clock rollback after claim keeps retry metadata monotone', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final remote = _GatedFailureRemote(OfflineRemoteErrorKind.unavailable);
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: testConfig(clock),
    );
    addTearDown(repository.dispose);
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'rollback-retry', value: VertexValue.nil()),
    );
    final draining = repository.drain('p');
    await remote.started.future;
    clock.advance(const Duration(minutes: -1));
    remote.release.complete();

    expect(await draining, 0);
    expect(remote.vertexPutCalls, 1);
    final retry = await store.transaction(
      (transaction) async => (await transaction.outbox('p')).single,
    );
    expect(retry.state, OfflineOutboxState.enqueued);
    expect(retry.nextAttemptAt, isNotNull);
    expect(retry.nextAttemptAt!.isBefore(retry.enqueuedAt), isFalse);
    final snapshot = await store.exportSnapshot();
    expect(() => InMemoryOfflineStore.fromSnapshot(snapshot), returnsNormally);
  });

  test('retry deadlines saturate inside the durable time range', () async {
    final nearMaximum = DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 998);
    final maximum = DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 999);
    final clock = MutableClock(nearMaximum);
    final store = InMemoryOfflineStore();
    final remote = FakeOfflineRemote()
      ..vertexPutFailures.add(failure(OfflineRemoteErrorKind.unavailable));
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (ceiling) => ceiling,
        baseRetryDelay: const Duration(seconds: 1),
        maxRetryDelay: const Duration(seconds: 1),
      ),
    );
    addTearDown(repository.dispose);
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'maximum-retry', value: VertexValue.nil()),
    );

    expect(await repository.drain('p'), 0);
    expect(remote.vertexPutCalls, 1);
    final retry = await store.transaction(
      (transaction) async => (await transaction.outbox('p')).single,
    );
    expect(retry.state, OfflineOutboxState.enqueued);
    expect(retry.nextAttemptAt, maximum);
    final snapshot = await store.exportSnapshot();
    expect(() => InMemoryOfflineStore.fromSnapshot(snapshot), returnsNormally);
  });

  test('expiration removes a pending overlay before replay', () async {
    final clock = MutableClock(initial);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(
        key: 'short',
        value: VertexValue.string('short'),
        expiresIn: const Duration(seconds: 1),
      ),
    );
    expect(
      (await repository.readVertex(
        'p',
        'short',
        policy: OfflineReadPolicy.cacheOnly,
      )).hasPendingWrites,
      isTrue,
    );
    clock.advance(const Duration(seconds: 1));
    final expiredOverlay = await repository.readVertex(
      'p',
      'short',
      policy: OfflineReadPolicy.cacheOnly,
    );
    expect(expiredOverlay.state, OfflineReadState.unknown);
    expect(expiredOverlay.value, isNull);
  });

  test(
    'authoritative Put outcomes preserve item order and terminalize exactly',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxDiagnosticCodeBytes: offlineMinimumDiagnosticCodeBytes,
        ),
      );
      await store.transaction<void>((transaction) async {
        await transaction.putCache(
          'p',
          OfflineCacheRecord.value(
            partitionId: 'p',
            generation: 0,
            key: const OfflineEntityKey.vertex('condition'),
            entity: Vertex(
              key: 'condition',
              value: VertexValue.string('old'),
              expiration: null,
            ),
            validatedAt: initial,
            lastAccessAt: initial,
          ),
        );
        await transaction.putCache(
          'p',
          OfflineCacheRecord.value(
            partitionId: 'p',
            generation: 0,
            key: const OfflineEntityKey.edge('tail', 'head'),
            entity: Edge(
              tail: 'tail',
              head: 'head',
              weight: 9,
              expiration: null,
            ),
            validatedAt: initial,
            lastAccessAt: initial,
          ),
        );
      });
      final remote = FakeOfflineRemote()
        ..vertexPutOutcomes.addAll(<PutOutcome>[
          PutOutcome.appliedAndLive,
          PutOutcome.expired,
          PutOutcome.conditionNotMet,
          PutOutcome.superseded,
        ])
        ..edgePutOutcomes.add(PutOutcome.expired);
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxConcurrency: 1,
          maxConcurrencyPerPartition: 1,
        ),
      );
      addTearDown(repository.dispose);
      final vertices = await repository.putVertices(
        partitionId: 'p',
        inputs: <VertexInput>[
          VertexInput(key: 'applied', value: VertexValue.string('new')),
          VertexInput(key: 'expired', value: VertexValue.string('new')),
          VertexInput(key: 'condition', value: VertexValue.string('new')),
          VertexInput(key: 'superseded', value: VertexValue.string('new')),
        ],
      );
      final edge = await repository.putEdge(
        partitionId: 'p',
        input: EdgeInput(tail: 'tail', head: 'head', weight: 1),
      );

      expect(await repository.drain('p'), 1);
      final vertexStatus = await repository.getWriteStatus(
        'p',
        vertices.operationId,
      );
      expect(vertexStatus!.items.map((item) => item.state), <OfflineWriteState>[
        OfflineWriteState.confirmed,
        OfflineWriteState.expired,
        OfflineWriteState.deadLetter,
        OfflineWriteState.deadLetter,
      ]);
      expect(
        vertexStatus.items.map((item) => item.attemptCount),
        everyElement(1),
      );
      expect(vertexStatus.items.map((item) => item.diagnosticCode), <String?>[
        null,
        'put_expired',
        'condition_not_met',
        'put_superseded',
      ]);
      final edgeStatus = await repository.getWriteStatus('p', edge.operationId);
      expect(edgeStatus!.items.single.state, OfflineWriteState.expired);
      expect(edgeStatus.items.single.attemptCount, 1);
      expect(edgeStatus.items.single.diagnosticCode, 'put_expired');

      final deadLetters = await store.transaction(
        (transaction) async => (await transaction.outbox('p'))
            .where((record) => record.state == OfflineOutboxState.deadLetter)
            .toList(growable: false),
      );
      expect(deadLetters, hasLength(2));
      expect(
        deadLetters.map((record) => record.deadLetteredAt),
        everyElement(initial),
      );
      expect(deadLetters.map((record) => record.diagnosticCode), <String?>[
        'condition_not_met',
        'put_superseded',
      ]);
      expect(
        deadLetters.map((record) => record.diagnosticCode!.length),
        everyElement(lessThanOrEqualTo(19)),
      );
      expect(
        (await repository.readVertex(
          'p',
          'condition',
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.unknown,
      );
      expect(
        (await repository.readEdge(
          'p',
          const EdgeRef('tail', 'head'),
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.unknown,
      );
    },
  );

  test(
    'response and commit expiration samples remain sticky across rollback',
    () async {
      Future<void> verify({required bool expireBeforeResponse}) async {
        final clock = MutableClock(initial);
        final store = _GateNextTransactionStore();
        final response = Completer<void>();
        final remote = _DelayedRemote(response);
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: testConfig(clock),
        );
        final write = await repository.putVertex(
          partitionId: 'p',
          input: VertexInput(
            key: expireBeforeResponse ? 'response' : 'commit',
            value: VertexValue.string('value'),
            expiresAt: initial.add(const Duration(seconds: 1)),
          ),
        );
        final draining = repository.drain('p');
        await remote.started.future;
        store.holdNext();
        if (expireBeforeResponse) {
          clock.advance(const Duration(seconds: 2));
        }
        response.complete();
        await store.blocked;
        if (expireBeforeResponse) {
          clock.now = initial;
        } else {
          clock.advance(const Duration(seconds: 2));
        }
        store.release();

        expect(await draining, 0);
        final status = await repository.getWriteStatus('p', write.operationId);
        expect(status!.items.single.state, OfflineWriteState.expired);
        expect(status.items.single.attemptCount, 1);
        expect(status.items.single.diagnosticCode, 'expired_at_commit');
        expect(
          (await repository.readVertex(
            'p',
            expireBeforeResponse ? 'response' : 'commit',
            policy: OfflineReadPolicy.cacheOnly,
          )).state,
          OfflineReadState.unknown,
        );
        await repository.dispose();
      }

      await verify(expireBeforeResponse: true);
      await verify(expireBeforeResponse: false);
    },
  );

  test(
    'observation expires idle work, reclaims capacity, and closes status',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxOutboxRecords: 1,
          maxOutboxRecordsPerPartition: 1,
        ),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final first = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'short',
          value: VertexValue.string('short'),
          expiresIn: const Duration(seconds: 1),
        ),
      );
      final statuses = first.statuses.toList();

      clock.advance(const Duration(seconds: 1));
      expect(await repository.listPending('p'), isEmpty);
      expect(
        (await repository.getWriteStatus(
          'p',
          first.operationId,
        ))!.items.single.state,
        OfflineWriteState.expired,
      );
      expect(
        (await statuses).map((status) => status.state),
        <OfflineWriteState>[
          OfflineWriteState.locallyCommitted,
          OfflineWriteState.expired,
        ],
      );
      expect(
        await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        ),
        isEmpty,
      );

      final second = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'next', value: VertexValue.string('next')),
      );
      expect(second.recordId, isNot(first.recordId));
      expect(await repository.listPending('p'), hasLength(1));
    },
  );

  test(
    'immediately expired enqueue returns a closed terminal stream',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxOutboxRecords: 0,
          maxOutboxRecordsPerPartition: 0,
        ),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);

      final handle = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'expired',
          value: VertexValue.string('expired'),
          expiresAt: initial,
        ),
      );
      final statuses = await handle.statuses.toList();
      expect(statuses, hasLength(1));
      expect(statuses.single.state, OfflineWriteState.expired);
      expect(
        await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        ),
        isEmpty,
      );
    },
  );

  test(
    'enqueue uses commit time without rebasing its absolute expiration',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxOutboxRecords: 1,
          maxOutboxRecordsPerPartition: 1,
        ),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final blockerStarted = Completer<void>();
      final releaseBlocker = Completer<void>();
      final blocker = store.transaction<void>((_) async {
        blockerStarted.complete();
        await releaseBlocker.future;
      });
      await blockerStarted.future;

      final pending = repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'crossed-before-commit',
          value: VertexValue.string('value'),
          expiresIn: const Duration(seconds: 1),
        ),
      );
      clock.advance(const Duration(seconds: 1));
      releaseBlocker.complete();
      await blocker;
      final first = await pending;

      expect(
        (await first.statuses.toList()).single.state,
        OfflineWriteState.expired,
      );
      expect(
        await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        ),
        isEmpty,
      );
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'capacity-reused', value: VertexValue.nil()),
      );
      expect(await repository.listPending('p'), hasLength(1));
    },
  );

  test('clock rollback cannot revive an already expired enqueue', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore(
      limits: const OfflineStoreLimits(
        maxOutboxRecords: 0,
        maxOutboxRecordsPerPartition: 0,
      ),
    );
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxWriteStatusControllers: 1,
      ),
    );
    addTearDown(repository.dispose);
    final blockerStarted = Completer<void>();
    final releaseBlocker = Completer<void>();
    final blocker = store.transaction<void>((_) async {
      blockerStarted.complete();
      await releaseBlocker.future;
    });
    await blockerStarted.future;

    final pending = repository.putVertex(
      partitionId: 'p',
      input: VertexInput(
        key: 'cannot-revive',
        value: VertexValue.string('value'),
        expiresAt: initial,
      ),
    );
    clock.advance(const Duration(seconds: -1));
    releaseBlocker.complete();
    await blocker;
    final handle = await pending;

    expect(
      (await handle.statuses.toList()).single.state,
      OfflineWriteState.expired,
    );
    expect(
      await store.transaction(
        (transaction) async => await transaction.outbox('p'),
      ),
      isEmpty,
    );
    expect(await repository.drain('p'), 0);
    expect(remote.vertexPutCalls, 0);
    final status = await repository.getWriteStatus('p', handle.operationId);
    expect(status!.items.single.attemptCount, 0);
    expect(status.items.single.diagnosticCode, 'expired');
    final snapshot = await store.exportSnapshot();
    expect(() => InMemoryOfflineStore.fromSnapshot(snapshot), returnsNormally);
  });

  test(
    'repository rejects out-of-range expirations without durable work',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);

      for (final expiration in <DateTime>[
        DateTime.utc(0),
        DateTime.utc(10000),
      ]) {
        await expectLater(
          repository.putVertex(
            partitionId: 'p',
            input: VertexInput(
              key: 'invalid-${expiration.year}',
              value: VertexValue.nil(),
              expiresAt: expiration,
            ),
          ),
          throwsA(isA<OfflineArgumentException>()),
        );
      }
      expect(
        await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        ),
        isEmpty,
      );
      expect(
        await store.transaction(
          (transaction) async => await transaction.operations('p'),
        ),
        isEmpty,
      );
    },
  );

  test(
    'observation uses transaction time after waiting behind the store',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final write = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'crossed-before-observation',
          value: VertexValue.string('value'),
          expiresIn: const Duration(seconds: 1),
        ),
      );
      final statuses = write.statuses.toList();
      final blockerStarted = Completer<void>();
      final releaseBlocker = Completer<void>();
      final blocker = store.transaction<void>((_) async {
        blockerStarted.complete();
        await releaseBlocker.future;
      });
      await blockerStarted.future;

      final observed = repository.getWriteStatus('p', write.operationId);
      clock.advance(const Duration(seconds: 1));
      releaseBlocker.complete();
      await blocker;

      expect((await observed)!.items.single.state, OfflineWriteState.expired);
      expect(
        (await statuses).map((status) => status.state),
        <OfflineWriteState>[
          OfflineWriteState.locallyCommitted,
          OfflineWriteState.expired,
        ],
      );
    },
  );

  test(
    'born-expired plural handles allocate no live status controllers',
    () async {
      final clock = MutableClock(initial);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxWriteStatusControllers: 1,
        ),
      );
      addTearDown(repository.dispose);

      final expired = await repository.putVertices(
        partitionId: 'p',
        inputs: List<VertexInput>.generate(
          64,
          (index) => VertexInput(
            key: 'expired-$index',
            value: VertexValue.string('expired'),
            expiresAt: initial,
          ),
        ),
      );
      expect(
        (await expired.items.last.statuses.toList()).single.state,
        OfflineWriteState.expired,
      );
      expect(
        (await expired.items.last.statuses.toList()).single.state,
        OfflineWriteState.expired,
      );

      final live = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'live', value: VertexValue.string('live')),
      );
      expect(
        (await live.statuses.first).state,
        OfflineWriteState.locallyCommitted,
      );
    },
  );

  test('watchWrite observes idle expiration and closes', () async {
    final clock = MutableClock(initial);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    addTearDown(repository.dispose);
    final write = await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(
        key: 'watched-expiration',
        value: VertexValue.string('value'),
        expiresIn: const Duration(seconds: 1),
      ),
    );
    clock.advance(const Duration(seconds: 1));

    final statuses = await repository
        .watchWrite('p', write.operationId)
        .toList();
    expect(statuses, hasLength(1));
    expect(statuses.single.items.single.state, OfflineWriteState.expired);
  });

  test(
    'watchWrite reports synchronous store capacity and releases watcher',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(maxChangeControllers: 1),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final held = store.changes('held').listen((_) {});
      final terminal = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'already-expired',
          value: VertexValue.nil(),
          expiresAt: initial,
        ),
      );

      await expectLater(
        repository.watchWrite('p', terminal.operationId).toList(),
        throwsA(isA<OfflineCapacityException>()),
      );
      await held.cancel();
      final recovered = await repository
          .watchWrite('p', terminal.operationId)
          .toList();
      expect(recovered.single.isTerminal, isTrue);
    },
  );

  test(
    'lazy maintenance scans a bounded page and prioritizes a target operation',
    () async {
      final clock = MutableClock(initial);
      final store = _InspectingStore(InMemoryOfflineStore());
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxAge: const Duration(hours: 1),
          maxSweepRecordsPerObservation: 2,
        ),
      );
      addTearDown(repository.dispose);
      for (var index = 0; index < 12; index++) {
        await repository.putVertex(
          partitionId: 'p',
          input: VertexInput(
            key: 'queued-$index',
            value: VertexValue.string('value'),
          ),
        );
      }
      clock.advance(const Duration(hours: 1));
      store.resetInspection();

      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'fresh', value: VertexValue.string('fresh')),
      );
      expect(store.outboxRecordsInspected, 2);
      expect(store.operationRecordsInspected, lessThanOrEqualTo(2));
      final retained = await store.inner.transaction(
        (transaction) async => await transaction.outbox('p'),
      );
      expect(
        retained.where(
          (record) => record.state == OfflineOutboxState.deadLetter,
        ),
        hasLength(2),
      );
      final target = retained.lastWhere(
        (record) =>
            record.state == OfflineOutboxState.enqueued &&
            record.intent.key.vertexKey != 'fresh',
      );
      store.resetInspection();

      final targetStatus = await repository.getWriteStatus(
        'p',
        target.operationId,
      );
      expect(store.outboxRecordsInspected, 1);
      expect(targetStatus!.items.single.state, OfflineWriteState.deadLetter);
    },
  );

  test(
    'operation deadline index finds an expired tail beyond the sweep limit',
    () async {
      final clock = MutableClock(initial);
      final store = _InspectingStore(InMemoryOfflineStore());
      await store.inner.transaction((transaction) async {
        final assigned = await transaction.enqueueAll(
          List<OfflineOutboxRecord>.generate(260, (index) {
            final isTail = index >= 256;
            return OfflineOutboxRecord(
              recordId: 'mixed-record-$index',
              operationId: 'mixed-large-operation',
              itemIndex: index,
              partitionId: 'p',
              intent: OfflinePutVertexIntent(
                Vertex(
                  key: 'mixed-key-$index',
                  value: VertexValue.string('value'),
                  expiration: isTail
                      ? initial.add(const Duration(seconds: 1))
                      : null,
                ),
              ),
              enqueuedAt: initial,
              ordinal: 0,
              state: isTail
                  ? OfflineOutboxState.enqueued
                  : OfflineOutboxState.deadLetter,
              attemptCount: 0,
              generation: 0,
              deadLetteredAt: isTail ? null : initial,
              diagnosticCode: isTail ? null : 'seed',
            );
          }),
        );
        await transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: 'mixed-large-operation',
            items: assigned
                .map(
                  (record) => OfflineWriteStatus(
                    recordId: record.recordId,
                    operationId: record.operationId,
                    itemIndex: record.itemIndex,
                    state: record.state == OfflineOutboxState.deadLetter
                        ? OfflineWriteState.deadLetter
                        : OfflineWriteState.locallyCommitted,
                    attemptCount: 0,
                    diagnosticCode: record.diagnosticCode,
                  ),
                )
                .toList(growable: false),
            updatedAt: initial,
          ),
        );
      });
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxSweepRecordsPerObservation: 2,
        ),
      );
      addTearDown(repository.dispose);
      clock.advance(const Duration(seconds: 1));
      store.resetInspection();

      final statuses = await repository
          .watchWrite('p', 'mixed-large-operation')
          .toList();
      expect(store.outboxRecordsInspected, 4);
      expect(store.maxOutboxBatchInspected, 2);
      expect(statuses.last.isTerminal, isTrue);
      expect(statuses.last.deadLetterCount, 256);
      expect(statuses.last.expiredCount, 4);
    },
  );

  test(
    'generated identity collisions exhaust a bounded retry atomically',
    () async {
      final clock = MutableClock(initial);
      final ids = <String>['op', 'record', 'op', 'record', 'op', 'record'];
      var index = 0;
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: () => ids[index++],
          jitter: (_) => Duration.zero,
          maxGeneratedIdAttempts: 2,
        ),
      );
      addTearDown(repository.dispose);
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'first', value: VertexValue.string('first')),
      );
      await expectLater(
        repository.putVertex(
          partitionId: 'p',
          input: VertexInput(
            key: 'second',
            value: VertexValue.string('second'),
          ),
        ),
        throwsA(isA<OfflineIdGenerationException>()),
      );
      expect(
        await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        ),
        hasLength(1),
      );
    },
  );

  test(
    'caller operation ID collisions never replace retained intent',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final original = await repository.putVertex(
        partitionId: 'p',
        operationId: 'caller-operation',
        input: VertexInput(key: 'original', value: VertexValue.string('value')),
      );
      for (final key in <String>['original', 'different']) {
        await expectLater(
          repository.putVertex(
            partitionId: 'p',
            operationId: 'caller-operation',
            input: VertexInput(key: key, value: VertexValue.string('value')),
          ),
          throwsA(
            isA<OfflineIdentityConflictException>().having(
              (error) => error.kind,
              'kind',
              OfflineIdentityKind.operation,
            ),
          ),
        );
      }
      final retained = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      expect(retained.recordId, original.recordId);
      expect(retained.intent.key.vertexKey, 'original');
    },
  );

  test(
    'terminal status controllers release their configured capacity',
    () async {
      final clock = MutableClock(initial);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxWriteStatusControllers: 1,
        ),
      );
      addTearDown(repository.dispose);
      final first = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'first', value: VertexValue.string('first')),
      );
      await expectLater(
        repository.putVertex(
          partitionId: 'p',
          input: VertexInput(
            key: 'blocked',
            value: VertexValue.string('blocked'),
          ),
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(await repository.drain('p'), 1);
      await Future<void>.delayed(Duration.zero);

      final lateStatuses = await first.statuses.toList();
      expect(lateStatuses.single.state, OfflineWriteState.confirmed);
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'after', value: VertexValue.string('after')),
      );
      expect(await repository.listPending('p'), hasLength(1));
    },
  );

  test(
    'wipe rejects late responses and canceled drain sends nothing',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final completer = Completer<void>();
      final remote = _DelayedRemote(completer);
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'late', value: VertexValue.string('late')),
      );
      final draining = repository.drain('p');
      final canceledDrain = expectLater(
        draining,
        throwsA(isA<OfflineCanceledException>()),
      );
      await remote.started.future;
      await repository.wipePartition('p');
      await canceledDrain;
      completer.complete();
      expect(
        await repository.readVertex(
          'p',
          'late',
          policy: OfflineReadPolicy.cacheOnly,
        ),
        isA<OfflineSnapshot<Vertex>>().having(
          (snapshot) => snapshot.state,
          'state',
          OfflineReadState.unknown,
        ),
      );

      final cancellation = LanternCancellationToken()..cancel();
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'cancel', value: VertexValue.string('cancel')),
      );
      await expectLater(
        repository.drain('p', cancellation: cancellation),
        throwsA(isA<OfflineCanceledException>()),
      );
    },
  );

  test('released claim exports a self-consistent restart snapshot', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final remote = _CancelableRemote();
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: testConfig(clock),
    );
    addTearDown(repository.dispose);
    final write = await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'cancel', value: VertexValue.string('cancel')),
    );
    final cancellation = LanternCancellationToken();
    final draining = repository.drain('p', cancellation: cancellation);
    await remote.started.future;
    cancellation.cancel();
    await expectLater(draining, throwsA(isA<OfflineCanceledException>()));

    final restored = InMemoryOfflineStore.fromSnapshot(
      await store.exportSnapshot(),
    );
    final record = await restored.transaction(
      (transaction) async => (await transaction.outbox('p')).single,
    );
    final operation = await restored.transaction(
      (transaction) async =>
          (await transaction.getOperation('p', write.operationId))!,
    );
    expect(record.diagnosticCode, 'canceled');
    expect(operation.items.single.diagnosticCode, 'canceled');
  });

  test('transport cancellation releases a consistent durable claim', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final remote = _DelayedRemote(Completer<void>());
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: testConfig(clock),
    );
    final operation = await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'cancel', value: VertexValue.nil()),
    );
    final cancellation = LanternCancellationToken();
    final draining = repository.drain('p', cancellation: cancellation);
    await remote.started.future;
    cancellation.cancel();

    await expectLater(draining, throwsA(isA<OfflineCanceledException>()));
    final durable = await store.transaction((transaction) async {
      return (
        outbox: (await transaction.outbox('p')).single,
        operation: (await transaction.getOperation(
          'p',
          operation.operationId,
        ))!,
      );
    });
    expect(durable.outbox.state, OfflineOutboxState.enqueued);
    expect(durable.outbox.leaseOwner, isNull);
    expect(durable.outbox.leaseUntil, isNull);
    expect(durable.outbox.attemptCount, 0);
    expect(durable.outbox.diagnosticCode, 'canceled');
    expect(
      durable.operation.items.single.state,
      OfflineWriteState.locallyCommitted,
    );
    expect(durable.operation.items.single.attemptCount, 0);
    expect(durable.operation.items.single.diagnosticCode, 'canceled');
    await repository.dispose();
  });

  test(
    'late responses after lease expiration are rejected then safely replayed',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final completer = Completer<void>();
      final remote = _DelayedRemote(completer);
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          leaseDuration: const Duration(seconds: 1),
        ),
      );
      final write = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'lease', value: VertexValue.string('lease')),
      );
      final draining = repository.drain('p');
      await remote.started.future;
      final leased = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      expect(leased.leaseUntil, initial.add(const Duration(seconds: 1)));
      clock.advance(const Duration(seconds: 1));
      expect(
        repository.config.clock(),
        initial.add(const Duration(seconds: 1)),
      );
      completer.complete();
      expect(await draining, 1);
      expect(remote.vertexPutCalls, 2);
      final status = await repository.getWriteStatus('p', write.operationId);
      expect(status!.items.single.state, OfflineWriteState.confirmed);
      expect(status.items.single.attemptCount, 1);
      expect(
        (await repository.readVertex(
          'p',
          'lease',
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.fresh,
      );
    },
  );

  test(
    'plural enqueue is atomic and shares operation ordering metadata',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      final operation = await repository.putVertices(
        partitionId: 'p',
        operationId: 'operation',
        inputs: <VertexInput>[
          VertexInput(
            key: 'a',
            value: VertexValue.string('a'),
            expiresIn: const Duration(minutes: 1),
          ),
          VertexInput(
            key: 'b',
            value: VertexValue.string('b'),
            expiresIn: const Duration(minutes: 1),
          ),
        ],
      );
      expect(operation.operationId, 'operation');
      expect(operation.itemCount, 2);
      final records = await store.transaction(
        (transaction) async => await transaction.outbox('p'),
      );
      expect(records.map((record) => record.ordinal), everyElement(1));
      expect(records.map((record) => record.itemIndex), <int>[0, 1]);
      expect(
        records.map((record) => record.absoluteExpiration),
        everyElement(initial.add(const Duration(minutes: 1))),
      );

      final bounded = OfflineLanternRepository(
        store: InMemoryOfflineStore(
          limits: const OfflineStoreLimits(
            maxOutboxRecords: 1,
            maxOutboxRecordsPerPartition: 1,
          ),
        ),
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      await expectLater(
        bounded.putVertices(
          partitionId: 'p',
          inputs: <VertexInput>[
            VertexInput(key: 'a', value: VertexValue.string('a')),
            VertexInput(key: 'b', value: VertexValue.string('b')),
          ],
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        await bounded.store.transaction(
          (transaction) async => await transaction.outbox('p'),
        ),
        isEmpty,
      );
    },
  );

  test('dead-letter authorization never holds the store transaction', () async {
    final clock = MutableClock(initial);
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'a',
      input: VertexInput(key: 'bad', value: VertexValue.string('bad')),
    );
    remote.vertexPutFailures.add(
      failure(OfflineRemoteErrorKind.invalidArgument),
    );
    await repository.drain('a');
    final dead = (await repository.listDeadLetters('a')).single;
    final authorization = Completer<bool>();
    final authorizerStarted = Completer<void>();
    final inspecting = repository.inspectDeadLetter(
      'a',
      dead.recordId,
      authorize: (_) {
        authorizerStarted.complete();
        return authorization.future;
      },
    );
    await authorizerStarted.future;
    await repository
        .putVertex(
          partitionId: 'b',
          input: VertexInput(key: 'free', value: VertexValue.string('free')),
        )
        .timeout(const Duration(seconds: 1));
    authorization.complete(true);
    expect(await inspecting, isA<OfflinePutVertexIntent>());
  });

  test(
    'blocked dead-letter authorization is quiesced before wipe and ID reuse',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'old', value: VertexValue.string('old')),
      );
      remote.vertexPutFailures.add(
        failure(OfflineRemoteErrorKind.invalidArgument),
      );
      await repository.drain('p');
      final old = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      final authorization = Completer<bool>();
      final authorizerStarted = Completer<void>();
      final inspection = repository.inspectDeadLetter(
        'p',
        old.recordId,
        authorize: (_) {
          authorizerStarted.complete();
          return authorization.future;
        },
      );
      final canceledInspection = expectLater(
        inspection,
        throwsA(isA<OfflineCanceledException>()),
      );
      await authorizerStarted.future;

      var wipeCompleted = false;
      final wiping = repository.wipePartition('p').whenComplete(() {
        wipeCompleted = true;
      });
      await canceledInspection;
      await wiping.timeout(const Duration(seconds: 1));
      expect(wipeCompleted, isTrue);

      await _seedDeadLetter(
        store,
        partitionId: 'p',
        recordId: old.recordId,
        operationId: old.operationId,
        key: 'new',
        now: initial,
      );
      authorization.complete(true);
      await Future<void>.delayed(Duration.zero);
      final replacement = await repository.inspectDeadLetter(
        'p',
        old.recordId,
        authorize: (_) async => true,
      );
      expect(
        replacement,
        isA<OfflinePutVertexIntent>().having(
          (intent) => intent.vertex.key,
          'key',
          'new',
        ),
      );
    },
  );

  test(
    'dead-letter inspection rejects a same ID reused during authorization',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      await _seedDeadLetter(
        store,
        partitionId: 'p',
        recordId: 'same-record',
        operationId: 'same-operation',
        key: 'old',
        now: initial,
      );

      final inspected = await repository.inspectDeadLetter(
        'p',
        'same-record',
        authorize: (_) async {
          await store.transaction((transaction) async {
            await transaction.wipePartition('p');
          });
          await _seedDeadLetter(
            store,
            partitionId: 'p',
            recordId: 'same-record',
            operationId: 'same-operation',
            key: 'new',
            now: initial,
          );
          return true;
        },
      );

      expect(inspected, isNull);
      expect(
        await repository.inspectDeadLetter(
          'p',
          'same-record',
          authorize: (_) async => true,
        ),
        isA<OfflinePutVertexIntent>().having(
          (intent) => intent.vertex.key,
          'key',
          'new',
        ),
      );
    },
  );

  test(
    'Store-facing recovery calls are quiesced by lifecycle closure',
    () async {
      Future<void> verify({
        required String name,
        required bool deadLetter,
        required bool dispose,
        required Future<void> Function(
          OfflineLanternRepository repository,
          OfflineWriteHandle handle,
          String recordId,
        )
        action,
      }) async {
        final clock = MutableClock(initial);
        final store = _GateNextTransactionStore();
        final remote = FakeOfflineRemote();
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: testConfig(clock),
        );
        final handle = await repository.putVertex(
          partitionId: 'p',
          input: VertexInput(key: name, value: VertexValue.string(name)),
        );
        if (deadLetter) {
          remote.vertexPutFailures.add(
            failure(OfflineRemoteErrorKind.invalidArgument),
          );
          await repository.drain('p');
        }
        final recordId = await store.transaction(
          (transaction) async =>
              (await transaction.outbox('p')).single.recordId,
        );
        store.holdNext();
        final active = action(repository, handle, recordId);
        await store.blocked;
        var lifecycleCompleted = false;
        final lifecycle =
            (dispose ? repository.dispose() : repository.wipePartition('p'))
                .whenComplete(() {
                  lifecycleCompleted = true;
                });
        await Future<void>.delayed(Duration.zero);
        expect(lifecycleCompleted, isFalse, reason: name);
        store.release();
        await active;
        await lifecycle;
        await repository.dispose();
      }

      await verify(
        name: 'get-status',
        deadLetter: false,
        dispose: false,
        action: (repository, handle, _) => repository
            .getWriteStatus('p', handle.operationId)
            .then<void>((status) => expect(status, isNotNull)),
      );
      await verify(
        name: 'list-pending',
        deadLetter: false,
        dispose: true,
        action: (repository, _, _) => repository
            .listPending('p')
            .then<void>((items) => expect(items, hasLength(1))),
      );
      await verify(
        name: 'list-dead-letters',
        deadLetter: true,
        dispose: false,
        action: (repository, _, _) => repository
            .listDeadLetters('p')
            .then<void>((items) => expect(items, hasLength(1))),
      );
      await verify(
        name: 'retry-dead-letter',
        deadLetter: true,
        dispose: true,
        action: (repository, _, recordId) =>
            repository.retryDeadLetter('p', recordId),
      );
      await verify(
        name: 'delete-dead-letter',
        deadLetter: true,
        dispose: false,
        action: (repository, _, recordId) =>
            repository.deleteDeadLetter('p', recordId),
      );
      await verify(
        name: 'auth-pause-status',
        deadLetter: false,
        dispose: true,
        action: (repository, _, _) => repository
            .isReplayPausedForAuth('p')
            .then<void>((paused) => expect(paused, isFalse)),
      );
    },
  );

  test(
    'wiping a prefix partition does not close another status stream',
    () async {
      final clock = MutableClock(initial);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      final handle = await repository.putVertex(
        partitionId: 'tenant:user',
        input: VertexInput(key: 'key', value: VertexValue.string('value')),
      );
      final statuses = handle.statuses.toList();
      await Future<void>.delayed(Duration.zero);
      await repository.wipePartition('tenant');
      expect(await repository.drain('tenant:user'), 1);
      expect((await statuses).last.state, OfflineWriteState.confirmed);
    },
  );

  test('a real probe is required before probe-triggered replay', () async {
    final clock = MutableClock(initial);
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'key', value: VertexValue.string('value')),
    );
    remote.probeFailures.add(failure(OfflineRemoteErrorKind.unavailable));
    await expectLater(
      repository.probeAndDrain('p'),
      throwsA(isA<OfflineRemoteFailure>()),
    );
    expect(remote.vertexPutCalls, 0);
    expect(await repository.probeAndDrain('p'), 1);
    expect(remote.probeCalls, 2);
  });

  test('expired dead-letter retry never returns work to replay', () async {
    final clock = MutableClock(initial);
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(
        key: 'short',
        value: VertexValue.string('short'),
        expiresIn: const Duration(seconds: 1),
      ),
    );
    remote.vertexPutFailures.add(
      failure(OfflineRemoteErrorKind.invalidArgument),
    );
    await repository.drain('p');
    final recordId = (await repository.listDeadLetters('p')).single.recordId;
    clock.advance(const Duration(seconds: 1));
    await repository.retryDeadLetter('p', recordId);
    expect(await repository.listPending('p'), isEmpty);
    expect(await repository.listDeadLetters('p'), isEmpty);
    expect(await repository.drain('p'), 0);
    expect(remote.vertexPutCalls, 1);
  });

  for (final remoteWouldFail in <bool>[false, true]) {
    test('max durable attempt count is terminal before wire '
        '(remoteWouldFail: $remoteWouldFail)', () async {
      const maximum = 0x7fffffffffffffff;
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      await store.transaction<void>((transaction) async {
        final assigned = await transaction.enqueue(
          OfflineOutboxRecord(
            recordId: 'max-attempt-record',
            operationId: 'max-attempt-operation',
            itemIndex: 0,
            partitionId: 'p',
            intent: OfflinePutVertexIntent(
              Vertex(
                key: 'max-attempt',
                value: VertexValue.nil(),
                expiration: null,
              ),
            ),
            enqueuedAt: initial,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: maximum,
            generation: 0,
          ),
        );
        await transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: assigned.operationId,
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: assigned.recordId,
                operationId: assigned.operationId,
                itemIndex: 0,
                state: OfflineWriteState.locallyCommitted,
                attemptCount: maximum,
              ),
            ],
            updatedAt: initial,
          ),
        );
      });
      final remote = FakeOfflineRemote();
      if (remoteWouldFail) {
        remote.vertexPutFailures.add(
          failure(OfflineRemoteErrorKind.unavailable),
        );
      }
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxAttempts: maximum,
        ),
      );
      addTearDown(repository.dispose);

      expect(await repository.drain('p'), 0);
      expect(remote.vertexPutCalls, 0);
      final terminal = await store.transaction(
        (transaction) async => (await transaction.outbox('p')).single,
      );
      expect(terminal.state, OfflineOutboxState.deadLetter);
      expect(terminal.attemptCount, maximum);
      expect(terminal.leaseOwner, isNull);
      expect(terminal.leaseUntil, isNull);
      expect(terminal.diagnosticCode, 'max_attempts');
      final snapshot = await store.exportSnapshot();
      expect(
        () => InMemoryOfflineStore.fromSnapshot(snapshot),
        returnsNormally,
      );
    });
  }

  test('a lower restart attempt budget terminalizes before wire', () async {
    const completedAttempts = 8;
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    await store.transaction<void>((transaction) async {
      final assigned = await transaction.enqueue(
        OfflineOutboxRecord(
          recordId: 'downgraded-attempt-record',
          operationId: 'downgraded-attempt-operation',
          itemIndex: 0,
          partitionId: 'p',
          intent: OfflinePutVertexIntent(
            Vertex(
              key: 'downgraded-attempt',
              value: VertexValue.nil(),
              expiration: null,
            ),
          ),
          enqueuedAt: initial,
          ordinal: 0,
          state: OfflineOutboxState.enqueued,
          attemptCount: completedAttempts,
          generation: 0,
        ),
      );
      await transaction.putOperation(
        OfflineOperationRecord(
          partitionId: 'p',
          generation: 0,
          operationId: assigned.operationId,
          items: <OfflineWriteStatus>[
            OfflineWriteStatus(
              recordId: assigned.recordId,
              operationId: assigned.operationId,
              itemIndex: 0,
              state: OfflineWriteState.locallyCommitted,
              attemptCount: completedAttempts,
            ),
          ],
          updatedAt: initial,
        ),
      );
    });
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore.fromSnapshot(await store.exportSnapshot()),
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxAttempts: completedAttempts,
      ),
    );
    addTearDown(repository.dispose);

    expect(await repository.drain('p'), 0);
    expect(remote.vertexPutCalls, 0);
    final deadLetters = await repository.listDeadLetters('p');
    expect(deadLetters.single.attemptCount, completedAttempts);
    expect(deadLetters.single.diagnosticCode, 'max_attempts');
  });

  test(
    'durable operation status survives repository and store restart',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final first = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      final operation = await first.putVertices(
        partitionId: 'p',
        operationId: 'durable-operation',
        inputs: <VertexInput>[
          VertexInput(key: 'a', value: VertexValue.string('a')),
          VertexInput(key: 'b', value: VertexValue.string('b')),
        ],
      );
      await first.dispose();

      final restarted = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      final transitions = restarted
          .watchWrite('p', operation.operationId)
          .toList();
      await Future<void>.delayed(Duration.zero);
      expect(
        (await restarted.getWriteStatus(
          'p',
          operation.operationId,
        ))!.items.map((item) => item.state),
        everyElement(OfflineWriteState.locallyCommitted),
      );
      expect(await restarted.drain('p'), 2);
      final observed = await transitions;
      expect(observed.first.isTerminal, isFalse);
      expect(observed.last.isTerminal, isTrue);
      expect(observed.last.confirmedCount, 2);

      final restoredStore = InMemoryOfflineStore.fromSnapshot(
        await store.exportSnapshot(),
      );
      final freshProcess = OfflineLanternRepository(
        store: restoredStore,
        remote: remote,
        config: testConfig(clock),
      );
      final durable = await freshProcess.getWriteStatus(
        'p',
        operation.operationId,
      );
      expect(durable!.isTerminal, isTrue);
      expect(durable.confirmedCount, 2);
    },
  );

  test(
    'mixed expiration aggregate remains deterministic across restart',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final first = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      final operation = await first.putVertices(
        partitionId: 'p',
        operationId: 'mixed-expiration',
        inputs: <VertexInput>[
          VertexInput(
            key: 'short',
            value: VertexValue.string('short'),
            expiresIn: const Duration(seconds: 1),
          ),
          VertexInput(key: 'live', value: VertexValue.string('live')),
        ],
      );
      clock.advance(const Duration(seconds: 1));
      expect(await first.listPending('p'), hasLength(1));
      await first.dispose();

      final restoredStore = InMemoryOfflineStore.fromSnapshot(
        await store.exportSnapshot(),
      );
      final restarted = OfflineLanternRepository(
        store: restoredStore,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(restarted.dispose);
      final before = await restarted.getWriteStatus('p', operation.operationId);
      expect(before!.items.map((item) => item.state), <OfflineWriteState>[
        OfflineWriteState.expired,
        OfflineWriteState.locallyCommitted,
      ]);
      expect(await restarted.drain('p'), 1);
      final after = await restarted.getWriteStatus('p', operation.operationId);
      expect(after!.isTerminal, isTrue);
      expect(after.expiredCount, 1);
      expect(after.confirmedCount, 1);
    },
  );

  test('diagnostic sink failures cannot interrupt write transitions', () async {
    final clock = MutableClock(initial);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: FakeOfflineRemote(),
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        diagnostics: const _ThrowingDiagnostics(),
      ),
    );
    final handle = await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'key', value: VertexValue.string('value')),
    );

    expect(await repository.drain('p'), 1);
    expect(
      (await repository.getWriteStatus(
        'p',
        handle.operationId,
      ))!.items.single.state,
      OfflineWriteState.confirmed,
    );
  });

  test('long remote work renews its lease before confirmation', () async {
    final clock = MutableClock(initial);
    final completion = Completer<void>();
    final remote = _DelayedRemote(completion);
    final store = _InspectingStore(InMemoryOfflineStore());
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        leaseDuration: const Duration(milliseconds: 60),
        leaseRenewalInterval: const Duration(milliseconds: 10),
      ),
    );
    addTearDown(repository.dispose);
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'lease', value: VertexValue.string('lease')),
    );
    final draining = repository.drain('p');
    await remote.started.future;
    clock.advance(const Duration(milliseconds: 40));
    await Future<void>.delayed(const Duration(milliseconds: 25));
    final renewed = await store.transaction(
      (transaction) async => (await transaction.outbox('p')).single.leaseUntil,
    );
    expect(renewed, initial.add(const Duration(milliseconds: 100)));
    clock.advance(const Duration(milliseconds: 40));
    completion.complete();

    expect(await draining, 1);
    expect(remote.vertexPutCalls, 1);
    final renewalsAtCompletion = store.leaseRenewals;
    clock.advance(const Duration(milliseconds: 40));
    await Future<void>.delayed(const Duration(milliseconds: 25));
    expect(
      store.leaseRenewals,
      renewalsAtCompletion,
      reason: 'a terminal send must cancel its lease-renewal Timer',
    );
  });

  test('disposed repositories reject new work', () async {
    final clock = MutableClock(initial);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    await repository.dispose();
    expect(
      () => repository.readVertex('p', 'key'),
      throwsA(isA<OfflineDisposedException>()),
    );
  });

  test(
    'dispose rejects an enqueue queued behind the store atomically',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      final blockerStarted = Completer<void>();
      final releaseBlocker = Completer<void>();
      final blocker = store.transaction<void>((_) async {
        blockerStarted.complete();
        await releaseBlocker.future;
      });
      await blockerStarted.future;
      final pending = repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'disposed-enqueue', value: VertexValue.nil()),
      );

      final disposing = repository.dispose();
      releaseBlocker.complete();
      await blocker;
      await disposing;
      await expectLater(pending, throwsA(isA<OfflineDisposedException>()));
      expect(
        await store.transaction(
          (transaction) async => await transaction.outbox('p'),
        ),
        isEmpty,
      );
      expect(
        await store.transaction(
          (transaction) async => await transaction.operations('p'),
        ),
        isEmpty,
      );
    },
  );

  test('dispose triggered by an enqueue commit returns its handle', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final repository = OfflineLanternRepository(
      store: store,
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    final disposalStarted = Completer<Future<void>>();
    final changes = store.changes('p').listen((_) {
      if (!disposalStarted.isCompleted) {
        disposalStarted.complete(repository.dispose());
      }
    });
    addTearDown(changes.cancel);

    final handle = await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(
        key: 'committed-before-dispose',
        value: VertexValue.nil(),
      ),
    );
    await (await disposalStarted.future);

    final records = await store.transaction(
      (transaction) async => await transaction.outbox('p'),
    );
    expect(records.single.recordId, handle.recordId);
    expect(
      (await handle.statuses.toList()).single.state,
      OfflineWriteState.locallyCommitted,
    );
    expect(
      () => repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'after-dispose', value: VertexValue.nil()),
      ),
      throwsA(isA<OfflineDisposedException>()),
    );
  });
}

Future<void> _seedDeadLetter(
  OfflineStore store, {
  required String partitionId,
  required String recordId,
  required String operationId,
  required String key,
  required DateTime now,
}) => store.transaction((transaction) async {
  final generation = await transaction.generation(partitionId);
  final record = await transaction.enqueue(
    OfflineOutboxRecord(
      recordId: recordId,
      operationId: operationId,
      itemIndex: 0,
      partitionId: partitionId,
      intent: OfflinePutVertexIntent(
        Vertex(key: key, value: VertexValue.string(key), expiration: null),
      ),
      enqueuedAt: now,
      ordinal: 0,
      state: OfflineOutboxState.deadLetter,
      attemptCount: 1,
      generation: generation,
      deadLetteredAt: now,
      diagnosticCode: 'invalid_argument',
    ),
  );
  await transaction.putOperation(
    OfflineOperationRecord(
      partitionId: partitionId,
      generation: generation,
      operationId: operationId,
      items: <OfflineWriteStatus>[
        OfflineWriteStatus(
          recordId: record.recordId,
          operationId: operationId,
          itemIndex: 0,
          state: OfflineWriteState.deadLetter,
          attemptCount: 1,
          diagnosticCode: 'invalid_argument',
        ),
      ],
      updatedAt: now,
      terminalAt: now,
    ),
  );
});

final class _GateNextTransactionStore implements OfflineStore {
  final InMemoryOfflineStore _delegate = InMemoryOfflineStore();
  Completer<void>? _nextGate;
  Completer<void>? _activeGate;
  Completer<void>? _blocked;

  Future<void> get blocked =>
      _blocked?.future ??
      Future<void>.error(StateError('no transaction is held'));

  void holdNext() {
    if (_nextGate != null || _activeGate != null) {
      throw StateError('a transaction is already held');
    }
    _nextGate = Completer<void>();
    _blocked = Completer<void>();
  }

  void release() {
    final gate = _activeGate;
    if (gate == null) throw StateError('no transaction is active');
    if (!gate.isCompleted) gate.complete();
  }

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      _delegate.changes(partitionId);

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) async {
    final gate = _nextGate;
    if (gate != null) {
      _nextGate = null;
      _activeGate = gate;
      final blocked = _blocked!;
      if (!blocked.isCompleted) blocked.complete();
      await gate.future;
      if (identical(_activeGate, gate)) _activeGate = null;
    }
    return _delegate.transaction(action);
  }
}

final class _ThrowingDiagnostics implements OfflineDiagnostics {
  const _ThrowingDiagnostics();

  @override
  void record(OfflineDiagnosticEvent event) {
    throw StateError('diagnostics unavailable');
  }
}

final class _ConcurrentRemote extends FakeOfflineRemote {
  final Completer<void> twoStarted = Completer<void>();
  final Completer<void> _release = Completer<void>();
  final Map<String, int> _activeByPartition = <String, int>{};
  final Map<String, int> maxActiveByPartition = <String, int>{};
  var active = 0;
  var maxActive = 0;

  void release() {
    if (!_release.isCompleted) _release.complete();
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    final partition = vertex.key.split('-').first;
    active += 1;
    _activeByPartition[partition] = (_activeByPartition[partition] ?? 0) + 1;
    maxActive = active > maxActive ? active : maxActive;
    final partitionActive = _activeByPartition[partition]!;
    final previous = maxActiveByPartition[partition] ?? 0;
    if (partitionActive > previous) {
      maxActiveByPartition[partition] = partitionActive;
    }
    if (active == 2 && !twoStarted.isCompleted) twoStarted.complete();
    final canceled = Completer<void>();
    final removeCancellation = cancellation?.listen((_) {
      if (!canceled.isCompleted) {
        canceled.completeError(failure(OfflineRemoteErrorKind.canceled));
      }
    });
    try {
      await Future.any<void>(<Future<void>>[_release.future, canceled.future]);
      vertices[vertex.key] = vertex;
      return PutOutcome.appliedAndLive;
    } finally {
      removeCancellation?.call();
      active -= 1;
      _activeByPartition[partition] = partitionActive - 1;
    }
  }
}

final class _CredentialRecordingRemote extends FakeOfflineRemote {
  _CredentialRecordingRemote(this.credentialProvider);

  final String Function() credentialProvider;
  final Completer<void> started = Completer<void>();
  final Completer<void> _release = Completer<void>();
  final Map<String, String> credentialsByKey = <String, String>{};

  Iterable<String> get credentialsForOldKeys => credentialsByKey.entries
      .where((entry) => entry.key.startsWith('old-'))
      .map((entry) => entry.value);

  void release() {
    if (!_release.isCompleted) _release.complete();
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    credentialsByKey[vertex.key] = credentialProvider();
    if (!started.isCompleted) started.complete();
    if (vertex.key.startsWith('old-')) {
      final canceled = Completer<void>();
      final removeCancellation = cancellation?.listen((_) {
        if (!canceled.isCompleted) {
          canceled.completeError(failure(OfflineRemoteErrorKind.canceled));
        }
      });
      try {
        await Future.any<void>(<Future<void>>[
          _release.future,
          canceled.future,
        ]);
      } finally {
        removeCancellation?.call();
      }
    }
    vertices[vertex.key] = vertex;
    return PutOutcome.appliedAndLive;
  }
}

final class _AuthGateRemote extends FakeOfflineRemote {
  final Completer<void> started = Completer<void>();
  final Completer<void> _release = Completer<void>();

  void releaseUnauthenticated() {
    if (!_release.isCompleted) _release.complete();
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls += 1;
    if (!started.isCompleted) started.complete();
    await _release.future;
    throw failure(OfflineRemoteErrorKind.unauthenticated);
  }
}

final class _FailNextTransactionStore implements OfflineStore {
  final InMemoryOfflineStore _delegate = InMemoryOfflineStore();
  var _failNext = false;

  void failNext() => _failNext = true;

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      _delegate.changes(partitionId);

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) {
    if (_failNext) {
      _failNext = false;
      return Future<T>.error(StateError('wipe failed'));
    }
    return _delegate.transaction(action);
  }
}

final class _DelayedRemote extends FakeOfflineRemote {
  _DelayedRemote(this.completer);

  final Completer<void> completer;
  final Completer<void> started = Completer<void>();

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    if (!started.isCompleted) started.complete();
    final canceled = Completer<void>();
    final removeCancellation = cancellation?.listen((_) {
      if (!canceled.isCompleted) {
        canceled.completeError(failure(OfflineRemoteErrorKind.canceled));
      }
    });
    try {
      await Future.any<void>(<Future<void>>[completer.future, canceled.future]);
      return PutOutcome.appliedAndLive;
    } finally {
      removeCancellation?.call();
    }
  }
}

final class _CancelableRemote extends FakeOfflineRemote {
  final Completer<void> started = Completer<void>();

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    if (!started.isCompleted) started.complete();
    final canceled = Completer<void>();
    final remove = cancellation?.listen((_) => canceled.complete());
    await canceled.future;
    remove?.call();
    throw failure(OfflineRemoteErrorKind.canceled);
  }
}

final class _GatedFailureRemote extends FakeOfflineRemote {
  _GatedFailureRemote([this.kind = OfflineRemoteErrorKind.invalidArgument]);

  final OfflineRemoteErrorKind kind;
  final Completer<void> started = Completer<void>();
  final Completer<void> release = Completer<void>();

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    started.complete();
    await release.future;
    throw failure(kind);
  }
}

final class _TransactionSignalingStore implements OfflineStore {
  _TransactionSignalingStore(this.inner);

  final InMemoryOfflineStore inner;
  Completer<void>? _nextTransaction;

  Future<void> signalNextTransaction() {
    final completer = Completer<void>();
    _nextTransaction = completer;
    return completer.future;
  }

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      inner.changes(partitionId);

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) {
    _nextTransaction?.complete();
    _nextTransaction = null;
    return inner.transaction(action);
  }
}

final class _InspectingStore implements OfflineStore {
  _InspectingStore(this.inner);

  final InMemoryOfflineStore inner;
  int outboxRecordsInspected = 0;
  int maxOutboxBatchInspected = 0;
  int operationRecordsInspected = 0;
  int leaseRenewals = 0;

  void resetInspection() {
    outboxRecordsInspected = 0;
    maxOutboxBatchInspected = 0;
    operationRecordsInspected = 0;
  }

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      inner.changes(partitionId);

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) => inner.transaction(
    (transaction) => action(_InspectingTransaction(this, transaction)),
  );
}

final class _InspectingTransaction implements OfflineStoreTransaction {
  const _InspectingTransaction(this.store, this.inner);

  final _InspectingStore store;
  final OfflineStoreTransaction inner;

  @override
  FutureOr<int> changeEpoch(String partitionId) =>
      inner.changeEpoch(partitionId);

  @override
  FutureOr<bool> hasUnknownResident(String partitionId, OfflineEntityKey key) =>
      inner.hasUnknownResident(partitionId, key);

  @override
  FutureOr<List<OfflineEntityKey>> unknownResidents(
    String partitionId, {
    required int limit,
  }) => inner.unknownResidents(partitionId, limit: limit);

  @override
  FutureOr<bool> completeUnknownResident(
    String partitionId,
    OfflineEntityKey key, {
    required int expectedEpoch,
  }) => inner.completeUnknownResident(
    partitionId,
    key,
    expectedEpoch: expectedEpoch,
  );

  @override
  Future<List<OfflineOutboxRecord>> dueOutbox(
    String partitionId, {
    String? operationId,
    OfflineEntityKey? key,
    required DateTime now,
    required Duration maxAge,
    required Duration deadLetterRetention,
    required int limit,
  }) async {
    final records = await inner.dueOutbox(
      partitionId,
      operationId: operationId,
      key: key,
      now: now,
      maxAge: maxAge,
      deadLetterRetention: deadLetterRetention,
      limit: limit,
    );
    store.outboxRecordsInspected += records.length;
    if (records.length > store.maxOutboxBatchInspected) {
      store.maxOutboxBatchInspected = records.length;
    }
    return records;
  }

  @override
  Future<List<OfflineOperationRecord>> dueOperations(
    String partitionId, {
    required DateTime now,
    required Duration retention,
    required int limit,
  }) async {
    final operations = await inner.dueOperations(
      partitionId,
      now: now,
      retention: retention,
      limit: limit,
    );
    store.operationRecordsInspected += operations.length;
    return operations;
  }

  @override
  Future<List<OfflineOutboxRecord>> claim(
    String partitionId, {
    required String owner,
    required DateTime now,
    required Duration maxAge,
    required Duration leaseDuration,
    required int limit,
  }) async => await inner.claim(
    partitionId,
    owner: owner,
    now: now,
    maxAge: maxAge,
    leaseDuration: leaseDuration,
    limit: limit,
  );

  @override
  Future<void> deleteCache(String partitionId, OfflineEntityKey key) async =>
      await inner.deleteCache(partitionId, key);

  @override
  Future<void> deleteOperation(String partitionId, String operationId) async =>
      await inner.deleteOperation(partitionId, operationId);

  @override
  Future<void> deleteOutbox(String partitionId, String recordId) async =>
      await inner.deleteOutbox(partitionId, recordId);

  @override
  Future<OfflineCacheRecord?> getCache(
    String partitionId,
    OfflineEntityKey key,
  ) async => await inner.getCache(partitionId, key);

  @override
  Future<OfflineOperationRecord?> getOperation(
    String partitionId,
    String operationId,
  ) async => await inner.getOperation(partitionId, operationId);

  @override
  Future<OfflineOutboxRecord?> getOutbox(
    String partitionId,
    String recordId,
  ) async => await inner.getOutbox(partitionId, recordId);

  @override
  Future<int> generation(String partitionId) async =>
      await inner.generation(partitionId);

  @override
  Future<bool> replayPausedForAuth(String partitionId) async =>
      await inner.replayPausedForAuth(partitionId);

  @override
  Future<void> setReplayPausedForAuth(String partitionId, bool paused) async =>
      await inner.setReplayPausedForAuth(partitionId, paused);

  @override
  Future<bool> hasOutboxForOperation(
    String partitionId,
    String operationId,
  ) async => await inner.hasOutboxForOperation(partitionId, operationId);

  @override
  Future<OfflineOutboxRecord> enqueue(OfflineOutboxRecord record) async =>
      await inner.enqueue(record);

  @override
  Future<List<OfflineOutboxRecord>> enqueueAll(
    List<OfflineOutboxRecord> records,
  ) async => await inner.enqueueAll(records);

  @override
  Future<List<OfflineOperationRecord>> operations(String partitionId) async =>
      await inner.operations(partitionId);

  @override
  Future<List<OfflineOutboxRecord>> outbox(String partitionId) async =>
      await inner.outbox(partitionId);

  @override
  Future<List<OfflineOutboxRecord>> outboxForKey(
    String partitionId,
    OfflineEntityKey key,
  ) async => await inner.outboxForKey(partitionId, key);

  @override
  Future<void> putCache(String partitionId, OfflineCacheRecord record) async =>
      await inner.putCache(partitionId, record);

  @override
  Future<void> putOperation(OfflineOperationRecord record) async =>
      await inner.putOperation(record);

  @override
  Future<bool> renewLease(
    String partitionId,
    String recordId, {
    required String owner,
    required int generation,
    required DateTime now,
    required Duration leaseDuration,
  }) async {
    store.leaseRenewals += 1;
    return await inner.renewLease(
      partitionId,
      recordId,
      owner: owner,
      generation: generation,
      now: now,
      leaseDuration: leaseDuration,
    );
  }

  @override
  Future<OfflineOperationScanPage> scanOperations(
    String partitionId, {
    String? afterOperationId,
    required int limit,
  }) async => await inner.scanOperations(
    partitionId,
    afterOperationId: afterOperationId,
    limit: limit,
  );

  @override
  Future<OfflineOutboxScanPage> scanOutbox(
    String partitionId, {
    OfflineOutboxCursor? after,
    String? operationId,
    OfflineEntityKey? key,
    required int limit,
  }) async => await inner.scanOutbox(
    partitionId,
    after: after,
    operationId: operationId,
    key: key,
    limit: limit,
  );

  @override
  Future<void> touchCache(
    String partitionId,
    OfflineEntityKey key,
    DateTime accessedAt,
  ) async => await inner.touchCache(partitionId, key, accessedAt);

  @override
  Future<void> updateOutbox(OfflineOutboxRecord record) async =>
      await inner.updateOutbox(record);

  @override
  Future<OfflineChangeCursor> changeCursor(String partitionId) async =>
      await inner.changeCursor(partitionId);

  @override
  Future<void> applyChangeChunk(
    String partitionId,
    OfflineChangeChunk chunk,
  ) async => await inner.applyChangeChunk(partitionId, chunk);

  @override
  Future<void> resetChangeCursor(
    String partitionId,
    OfflineChangeCursor checkpoint,
  ) async => await inner.resetChangeCursor(partitionId, checkpoint);

  @override
  Future<void> wipePartition(String partitionId) async =>
      await inner.wipePartition(partitionId);
}
