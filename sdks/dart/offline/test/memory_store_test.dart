import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

void main() {
  final now = DateTime.utc(2026, 7, 22);

  OfflineOutboxRecord record(String id, String key) => OfflineOutboxRecord(
    recordId: id,
    operationId: 'op-$id',
    itemIndex: 0,
    partitionId: 'p',
    intent: OfflinePutVertexIntent(
      Vertex(key: key, value: VertexValue.string(key), expiration: null),
    ),
    enqueuedAt: now,
    ordinal: 0,
    state: OfflineOutboxState.enqueued,
    attemptCount: 0,
    generation: 0,
  );

  test('transactions are atomic and assign monotone ordinals', () async {
    final store = InMemoryOfflineStore();
    await expectLater(
      store.transaction<void>((transaction) {
        transaction.enqueue(record('discarded', 'a'));
        throw StateError('abort');
      }),
      throwsStateError,
    );
    expect(
      await store.transaction((transaction) => transaction.outbox('p')),
      isEmpty,
    );
    final assigned = await store.transaction(
      (transaction) => transaction.enqueue(record('kept', 'a')),
    );
    expect(assigned.ordinal, 1);
  });

  test(
    'claims independent keys while preserving per-key FIFO and lease',
    () async {
      final store = InMemoryOfflineStore();
      await store.transaction((transaction) {
        transaction.enqueue(record('one', 'a'));
        transaction.enqueue(record('two', 'a'));
        transaction.enqueue(record('three', 'b'));
      });
      final first = await store.transaction(
        (transaction) => transaction.claim(
          'p',
          owner: 'owner',
          now: now,
          leaseDuration: const Duration(seconds: 1),
          limit: 2,
        ),
      );
      expect(first.map((item) => item.recordId), <String>['one', 'three']);
      final blocked = await store.transaction(
        (transaction) => transaction.claim(
          'p',
          owner: 'other',
          now: now,
          leaseDuration: const Duration(seconds: 1),
          limit: 2,
        ),
      );
      expect(blocked, isEmpty);
      final recovered = await store.transaction(
        (transaction) => transaction.claim(
          'p',
          owner: 'recovered',
          now: now.add(const Duration(seconds: 1)),
          leaseDuration: const Duration(seconds: 1),
          limit: 2,
        ),
      );
      expect(recovered.map((item) => item.recordId), <String>['one', 'three']);
      await store.transaction((transaction) {
        transaction.deleteOutbox('p', 'one');
        transaction.deleteOutbox('p', 'three');
      });
      final next = await store.transaction(
        (transaction) => transaction.claim(
          'p',
          owner: 'next',
          now: now.add(const Duration(seconds: 2)),
          leaseDuration: const Duration(seconds: 1),
          limit: 2,
        ),
      );
      expect(next.single.recordId, 'two');
    },
  );

  test(
    'capacity evicts confirmed cache but never unconfirmed outbox',
    () async {
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxCacheRecords: 1,
          maxCacheBytes: 10000,
          maxOutboxRecords: 1,
          maxOutboxBytes: 10000,
        ),
      );
      await store.transaction((transaction) {
        transaction.putCache(
          'p',
          OfflineCacheRecord.value(
            partitionId: 'p',
            generation: 0,
            key: const OfflineEntityKey.vertex('a'),
            entity: Vertex(
              key: 'a',
              value: VertexValue.string('a'),
              expiration: null,
            ),
            validatedAt: now,
            lastAccessAt: now,
          ),
        );
        transaction.putCache(
          'p',
          OfflineCacheRecord.value(
            partitionId: 'p',
            generation: 0,
            key: const OfflineEntityKey.vertex('b'),
            entity: Vertex(
              key: 'b',
              value: VertexValue.string('b'),
              expiration: null,
            ),
            validatedAt: now,
            lastAccessAt: now.add(const Duration(seconds: 1)),
          ),
        );
        transaction.enqueue(record('one', 'one'));
      });
      expect(
        await store.transaction(
          (transaction) =>
              transaction.getCache('p', const OfflineEntityKey.vertex('a')),
        ),
        isNull,
      );
      await expectLater(
        store.transaction(
          (transaction) => transaction.enqueue(record('two', 'two')),
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        await store.transaction((transaction) => transaction.outbox('p')),
        hasLength(1),
      );
    },
  );

  test(
    'wipe increments generation and removes cache, outbox, and leases',
    () async {
      final store = InMemoryOfflineStore();
      await store.transaction((transaction) {
        transaction.enqueue(record('one', 'a'));
        transaction.wipePartition('p');
      });
      expect(
        await store.transaction((transaction) => transaction.generation('p')),
        1,
      );
      expect(
        await store.transaction((transaction) => transaction.outbox('p')),
        isEmpty,
      );
    },
  );

  test(
    'cache access updates LRU without publishing a semantic mutation',
    () async {
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxCacheRecords: 2,
          maxCacheBytes: 10000,
        ),
      );
      OfflineCacheRecord cache(String key, DateTime access) =>
          OfflineCacheRecord.value(
            partitionId: 'p',
            generation: 0,
            key: OfflineEntityKey.vertex(key),
            entity: Vertex(
              key: key,
              value: VertexValue.string(key),
              expiration: null,
            ),
            validatedAt: now,
            lastAccessAt: access,
          );
      await store.transaction((transaction) {
        transaction.putCache('p', cache('a', now));
        transaction.putCache('p', cache('b', now));
        transaction.touchCache(
          'p',
          const OfflineEntityKey.vertex('a'),
          now.add(const Duration(seconds: 1)),
        );
        transaction.putCache(
          'p',
          cache('c', now.add(const Duration(seconds: 2))),
        );
      });
      expect(
        await store.transaction(
          (transaction) =>
              transaction.getCache('p', const OfflineEntityKey.vertex('a')),
        ),
        isNotNull,
      );
      expect(
        await store.transaction(
          (transaction) =>
              transaction.getCache('p', const OfflineEntityKey.vertex('b')),
        ),
        isNull,
      );
    },
  );

  test('state transitions cannot be wedged by enqueue byte capacity', () async {
    final expected = OfflineOutboxRecord(
      recordId: 'one',
      operationId: 'op-one',
      itemIndex: 0,
      partitionId: 'p',
      intent: OfflinePutVertexIntent(
        Vertex(key: 'a', value: VertexValue.string('a'), expiration: null),
      ),
      enqueuedAt: now,
      ordinal: 1,
      state: OfflineOutboxState.enqueued,
      attemptCount: 0,
      generation: 0,
    );
    final admittedBytes = utf8
        .encode(OfflineCodec.encodeOutboxRecord(expected))
        .length;
    final store = InMemoryOfflineStore(
      limits: OfflineStoreLimits(
        maxOutboxBytes: admittedBytes,
        maxOutboxBytesPerPartition: admittedBytes,
      ),
    );
    await store.transaction((transaction) {
      transaction.enqueue(record('one', 'a'));
    });
    final claimed = await store.transaction(
      (transaction) => transaction.claim(
        'p',
        owner: 'owner-with-longer-metadata',
        now: now,
        leaseDuration: const Duration(seconds: 30),
        limit: 1,
      ),
    );
    await store.transaction((transaction) {
      transaction.updateOutbox(
        claimed.single.copyWith(
          state: OfflineOutboxState.enqueued,
          attemptCount: 1,
          nextAttemptAt: now.add(const Duration(seconds: 1)),
          clearLeaseOwner: true,
          clearLeaseUntil: true,
          diagnosticCode: 'unavailable',
        ),
      );
    });
    final retriable = await store.transaction(
      (transaction) => transaction.outbox('p').single,
    );
    expect(retriable.state, OfflineOutboxState.enqueued);
    expect(retriable.diagnosticCode, 'unavailable');
  });

  test('global caps apply across partitions without evicting outbox', () async {
    final store = InMemoryOfflineStore(
      limits: const OfflineStoreLimits(
        maxCacheRecords: 1,
        maxCacheRecordsPerPartition: 2,
        maxCacheBytes: 10000,
        maxCacheBytesPerPartition: 10000,
        maxOutboxRecords: 1,
        maxOutboxRecordsPerPartition: 2,
        maxOutboxBytes: 10000,
        maxOutboxBytesPerPartition: 10000,
      ),
    );
    OfflineCacheRecord cache(String partition, String key) =>
        OfflineCacheRecord.value(
          partitionId: partition,
          generation: 0,
          key: OfflineEntityKey.vertex(key),
          entity: Vertex(
            key: key,
            value: VertexValue.string(key),
            expiration: null,
          ),
          validatedAt: now,
          lastAccessAt: now,
        );
    await store.transaction((transaction) {
      transaction.putCache('a', cache('a', 'one'));
      transaction.putCache('b', cache('b', 'two'));
    });
    expect(
      await store.transaction(
        (transaction) =>
            transaction.getCache('a', const OfflineEntityKey.vertex('one')),
      ),
      isNull,
    );
    await store.transaction((transaction) {
      transaction.enqueue(
        OfflineOutboxRecord(
          recordId: 'a',
          operationId: 'op-a',
          itemIndex: 0,
          partitionId: 'a',
          intent: OfflinePutVertexIntent(
            Vertex(key: 'a', value: VertexValue.string('a'), expiration: null),
          ),
          enqueuedAt: now,
          ordinal: 0,
          state: OfflineOutboxState.enqueued,
          attemptCount: 0,
          generation: 0,
        ),
      );
    });
    await expectLater(
      store.transaction((transaction) {
        transaction.enqueue(
          OfflineOutboxRecord(
            recordId: 'b',
            operationId: 'op-b',
            itemIndex: 0,
            partitionId: 'b',
            intent: OfflinePutVertexIntent(
              Vertex(
                key: 'b',
                value: VertexValue.string('b'),
                expiration: null,
              ),
            ),
            enqueuedAt: now,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: 0,
            generation: 0,
          ),
        );
      }),
      throwsA(isA<OfflineCapacityException>()),
    );
    expect(
      await store.transaction((transaction) => transaction.outbox('a')),
      hasLength(1),
    );
  });

  test('plural enqueue shares one ordinal and is all-or-nothing', () async {
    final store = InMemoryOfflineStore();
    OfflineOutboxRecord item(int index, String key) => OfflineOutboxRecord(
      recordId: 'record-$index',
      operationId: 'operation',
      itemIndex: index,
      partitionId: 'p',
      intent: OfflinePutVertexIntent(
        Vertex(key: key, value: VertexValue.string(key), expiration: null),
      ),
      enqueuedAt: now,
      ordinal: 0,
      state: OfflineOutboxState.enqueued,
      attemptCount: 0,
      generation: 0,
    );
    final assigned = await store.transaction(
      (transaction) => transaction.enqueueAll(<OfflineOutboxRecord>[
        item(0, 'a'),
        item(1, 'b'),
      ]),
    );
    expect(assigned.map((record) => record.ordinal), everyElement(1));
    expect(assigned.map((record) => record.itemIndex), <int>[0, 1]);

    await expectLater(
      store.transaction((transaction) {
        transaction.enqueueAll(<OfflineOutboxRecord>[
          item(0, 'c'),
          item(2, 'd'),
        ]);
      }),
      throwsA(isA<OfflineArgumentException>()),
    );
    expect(
      await store.transaction((transaction) => transaction.outbox('p')),
      hasLength(2),
    );
  });

  test('cache rejects a mismatched partition generation', () async {
    final store = InMemoryOfflineStore();
    await expectLater(
      store.transaction((transaction) {
        transaction.putCache(
          'p',
          OfflineCacheRecord.value(
            partitionId: 'other',
            generation: 0,
            key: const OfflineEntityKey.vertex('key'),
            entity: Vertex(
              key: 'key',
              value: VertexValue.string('value'),
              expiration: null,
            ),
            validatedAt: now,
            lastAccessAt: now,
          ),
        );
      }),
      throwsA(isA<OfflineArgumentException>()),
    );
  });

  test(
    'canonical snapshot survives a fresh Dart process and lease recovery',
    () async {
      final store = InMemoryOfflineStore();
      await store.transaction((transaction) {
        transaction.putCache(
          'p',
          OfflineCacheRecord.value(
            partitionId: 'p',
            generation: 0,
            key: const OfflineEntityKey.vertex('cached'),
            entity: Vertex(
              key: 'cached',
              value: VertexValue.bytes(Uint8List.fromList(<int>[251, 255])),
              expiration: now.add(const Duration(minutes: 1)),
            ),
            validatedAt: now,
            lastAccessAt: now,
          ),
        );
        final assigned = transaction.enqueue(
          OfflineOutboxRecord(
            recordId: 'put-edge',
            operationId: 'operation',
            itemIndex: 0,
            partitionId: 'p',
            intent: OfflinePutEdgeIntent(
              Edge(
                tail: 'tail',
                head: 'head',
                weight: Float32Value(0.1).value,
                expiration: now.add(const Duration(minutes: 1)),
              ),
            ),
            enqueuedAt: now,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: 0,
            generation: 0,
          ),
        );
        final claimed = transaction.claim(
          'p',
          owner: 'crashed-process',
          now: now,
          leaseDuration: const Duration(seconds: 1),
          limit: 1,
        );
        transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: 'operation',
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: assigned.recordId,
                operationId: assigned.operationId,
                itemIndex: assigned.itemIndex,
                state: OfflineWriteState.sending,
                attemptCount: claimed.single.attemptCount,
              ),
            ],
            updatedAt: now,
          ),
        );
      });
      final snapshot = await store.exportSnapshot();
      final restored = InMemoryOfflineStore.fromSnapshot(snapshot);
      expect(await restored.exportSnapshot(), snapshot);
      expect(
        await restored.transaction(
          (transaction) => transaction.claim(
            'p',
            owner: 'too-early',
            now: now,
            leaseDuration: const Duration(seconds: 1),
            limit: 1,
          ),
        ),
        isEmpty,
      );
      final recovered = await restored.transaction(
        (transaction) => transaction.claim(
          'p',
          owner: 'restarted-process',
          now: now.add(const Duration(seconds: 1)),
          leaseDuration: const Duration(seconds: 1),
          limit: 1,
        ),
      );
      expect(recovered.single.intent, isA<OfflinePutEdgeIntent>());

      final temporary = await Directory.systemTemp.createTemp(
        'lantern-offline-snapshot-',
      );
      addTearDown(() => temporary.delete(recursive: true));
      final path = '${temporary.path}/snapshot.json';
      await File(path).writeAsString(snapshot, flush: true);
      final process = await Process.run(Platform.resolvedExecutable, <String>[
        'run',
        'test/support/snapshot_verifier.dart',
        path,
      ], workingDirectory: Directory.current.path);
      expect(process.exitCode, 0, reason: '${process.stderr}');
      expect(process.stdout, snapshot);
    },
  );

  test('snapshot restore fails closed on schema and corruption', () {
    expect(
      () => InMemoryOfflineStore.fromSnapshot('{"schema":4,"partitions":[]}'),
      throwsA(isA<OfflineSchemaException>()),
    );
    expect(
      () => InMemoryOfflineStore.fromSnapshot(
        '{"schema":1,"partitions":[{"partitionId":"p","generation":0,"version":0,"nextOrdinal":0,"cache":["corrupt"],"outbox":[]}]}',
      ),
      throwsA(isA<OfflineCodecException>()),
    );
  });

  test('schema v1 migration reconstructs active operation metadata', () async {
    final store = InMemoryOfflineStore();
    await store.transaction((transaction) {
      final records = transaction.enqueueAll(<OfflineOutboxRecord>[
        OfflineOutboxRecord(
          recordId: 'confirmed-before-snapshot',
          operationId: 'legacy-operation',
          itemIndex: 0,
          partitionId: 'p',
          intent: OfflinePutVertexIntent(
            Vertex(key: 'a', value: VertexValue.string('a'), expiration: null),
          ),
          enqueuedAt: now,
          ordinal: 0,
          state: OfflineOutboxState.enqueued,
          attemptCount: 0,
          generation: 0,
        ),
        OfflineOutboxRecord(
          recordId: 'still-pending',
          operationId: 'legacy-operation',
          itemIndex: 1,
          partitionId: 'p',
          intent: OfflinePutVertexIntent(
            Vertex(key: 'b', value: VertexValue.string('b'), expiration: null),
          ),
          enqueuedAt: now,
          ordinal: 0,
          state: OfflineOutboxState.enqueued,
          attemptCount: 0,
          generation: 0,
        ),
      ]);
      transaction.deleteOutbox('p', records.first.recordId);
    });
    final v2 = jsonDecode(await store.exportSnapshot()) as Map<String, Object?>;
    v2['schema'] = 1;
    final partitions = v2['partitions']! as List<Object?>;
    for (final value in partitions) {
      (value! as Map<String, Object?>).remove('operations');
    }
    final restored = InMemoryOfflineStore.fromSnapshot(jsonEncode(v2));
    final operation = await restored.transaction(
      (transaction) =>
          transaction.getOperation('p', 'legacy-operation')!.status,
    );

    expect(operation.items, hasLength(2));
    expect(operation.items.first.state, OfflineWriteState.outcomeUnknown);
    expect(operation.items.last.state, OfflineWriteState.locallyCommitted);
    expect(
      jsonDecode(await restored.exportSnapshot()),
      isA<Map<String, Object?>>().having(
        (value) => value['schema'],
        'schema',
        InMemoryOfflineStore.snapshotSchemaVersion,
      ),
    );
  });

  test(
    'schema v1 migration quarantines legacy Add while reconstructing status',
    () async {
      final store = InMemoryOfflineStore();
      await store.transaction((transaction) {
        transaction.enqueue(
          OfflineOutboxRecord(
            recordId: 'legacy-v1-add',
            operationId: 'legacy-v1-operation',
            itemIndex: 0,
            partitionId: 'p',
            intent: OfflineAddEdgeIntent(
              Edge(
                tail: 'tail',
                head: 'head',
                weight: Float32Value(0.25).value,
                expiration: now.add(const Duration(minutes: 1)),
              ),
              Uint8List.fromList(List<int>.generate(24, (index) => index + 1)),
            ),
            enqueuedAt: now,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: 2,
            generation: 0,
            diagnosticCode: 'unavailable',
          ),
        );
        expect(
          transaction.claim(
            'p',
            owner: 'legacy-process',
            now: now,
            leaseDuration: const Duration(minutes: 1),
            limit: 1,
          ),
          hasLength(1),
        );
      });
      final legacy =
          jsonDecode(await store.exportSnapshot()) as Map<String, Object?>;
      legacy['schema'] = 1;
      for (final value in legacy['partitions']! as List<Object?>) {
        (value! as Map<String, Object?>).remove('operations');
      }

      final restored = InMemoryOfflineStore.fromSnapshot(jsonEncode(legacy));
      final migrated = await restored.transaction((transaction) {
        return (
          record: transaction.getOutbox('p', 'legacy-v1-add')!,
          operation: transaction
              .getOperation('p', 'legacy-v1-operation')!
              .status,
          claimed: transaction.claim(
            'p',
            owner: 'restarted-process',
            now: now.add(const Duration(minutes: 2)),
            leaseDuration: const Duration(seconds: 1),
            limit: 1,
          ),
        );
      });

      expect(migrated.record.state, OfflineOutboxState.deadLetter);
      expect(migrated.record.attemptCount, 2);
      expect(migrated.record.leaseOwner, isNull);
      expect(migrated.record.leaseUntil, isNull);
      expect(migrated.record.diagnosticCode, 'unsupported_add');
      expect(migrated.operation.isTerminal, isTrue);
      expect(
        migrated.operation.items.single.state,
        OfflineWriteState.deadLetter,
      );
      expect(migrated.operation.items.single.attemptCount, 2);
      expect(migrated.operation.items.single.diagnosticCode, 'unsupported_add');
      expect(migrated.claimed, isEmpty);
    },
  );

  test(
    'schema v2 migration quarantines legacy Add as inspectable terminal work',
    () async {
      final store = InMemoryOfflineStore();
      await store.transaction((transaction) {
        final assigned = transaction.enqueue(
          OfflineOutboxRecord(
            recordId: 'legacy-add',
            operationId: 'legacy-add-operation',
            itemIndex: 0,
            partitionId: 'p',
            intent: OfflineAddEdgeIntent(
              Edge(
                tail: 'tail',
                head: 'head',
                weight: Float32Value(0.25).value,
                expiration: now.add(const Duration(minutes: 1)),
              ),
              Uint8List.fromList(List<int>.generate(24, (index) => index + 1)),
            ),
            enqueuedAt: now,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: 0,
            generation: 0,
          ),
        );
        final claimed = transaction.claim(
          'p',
          owner: 'old-process',
          now: now,
          leaseDuration: const Duration(minutes: 1),
          limit: 1,
        );
        transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: assigned.operationId,
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: assigned.recordId,
                operationId: assigned.operationId,
                itemIndex: 0,
                state: OfflineWriteState.sending,
                attemptCount: claimed.single.attemptCount,
              ),
            ],
            updatedAt: now,
          ),
        );
      });
      final legacy = jsonDecode(await store.exportSnapshot());
      (legacy as Map<String, Object?>)['schema'] = 2;

      final restored = InMemoryOfflineStore.fromSnapshot(jsonEncode(legacy));
      final migrated = await restored.transaction((transaction) {
        return (
          record: transaction.getOutbox('p', 'legacy-add')!,
          operation: transaction
              .getOperation('p', 'legacy-add-operation')!
              .status,
          claimed: transaction.claim(
            'p',
            owner: 'new-process',
            now: now.add(const Duration(minutes: 2)),
            leaseDuration: const Duration(seconds: 1),
            limit: 1,
          ),
        );
      });

      expect(migrated.record.state, OfflineOutboxState.deadLetter);
      expect(migrated.record.attemptCount, 0);
      expect(migrated.record.leaseOwner, isNull);
      expect(migrated.record.leaseUntil, isNull);
      expect(migrated.record.diagnosticCode, 'unsupported_add');
      expect(
        migrated.operation.items.single.state,
        OfflineWriteState.deadLetter,
      );
      expect(migrated.operation.items.single.diagnosticCode, 'unsupported_add');
      expect(migrated.operation.isTerminal, isTrue);
      expect(migrated.claimed, isEmpty);
      expect(
        (jsonDecode(await restored.exportSnapshot())
            as Map<String, Object?>)['schema'],
        InMemoryOfflineStore.snapshotSchemaVersion,
      );
    },
  );

  test(
    'reference store passes the reusable adapter conformance suite',
    () async {
      await runStoreConformanceSuite(InMemoryOfflineStore.new);
    },
  );

  test(
    'operation capacity evicts terminal metadata but never active status',
    () async {
      OfflineOperationRecord operation(
        String id,
        OfflineWriteState state, {
        DateTime? terminalAt,
      }) => OfflineOperationRecord(
        partitionId: 'p',
        generation: 0,
        operationId: id,
        items: <OfflineWriteStatus>[
          OfflineWriteStatus(
            recordId: 'record-$id',
            operationId: id,
            itemIndex: 0,
            state: state,
            attemptCount: 0,
          ),
        ],
        updatedAt: now,
        terminalAt: terminalAt,
      );

      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxOperationRecords: 1,
          maxOperationRecordsPerPartition: 1,
        ),
      );
      await store.transaction<void>(
        (transaction) => transaction.putOperation(
          operation('active', OfflineWriteState.locallyCommitted),
        ),
      );
      await expectLater(
        store.transaction<void>(
          (transaction) => transaction.putOperation(
            operation('second', OfflineWriteState.locallyCommitted),
          ),
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        await store.transaction(
          (transaction) => transaction.operations('p').single.operationId,
        ),
        'active',
      );

      await store.transaction<void>(
        (transaction) => transaction.putOperation(
          operation('active', OfflineWriteState.confirmed, terminalAt: now),
        ),
      );
      await store.transaction<void>(
        (transaction) => transaction.putOperation(
          operation('second', OfflineWriteState.locallyCommitted),
        ),
      );
      expect(
        await store.transaction(
          (transaction) => transaction.operations('p').single.operationId,
        ),
        'second',
      );
    },
  );
}
