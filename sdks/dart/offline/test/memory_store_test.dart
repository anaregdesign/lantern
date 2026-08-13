import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

import 'helpers.dart';

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

  test('post-commit transaction references are sealed', () async {
    final store = InMemoryOfflineStore();
    late OfflineStoreTransaction escaped;
    await store.transaction<void>((transaction) {
      escaped = transaction;
      transaction.enqueue(record('committed', 'a'));
    });

    expect(
      () => escaped.generation('p'),
      throwsA(isA<OfflineTransactionClosedException>()),
    );
    expect(
      () => escaped.deleteOutbox('p', 'committed'),
      throwsA(isA<OfflineTransactionClosedException>()),
    );
    expect(
      await store.transaction((transaction) => transaction.outbox('p')),
      hasLength(1),
    );
  });

  test('write status metadata rejects invalid public durable state', () {
    expect(
      () => OfflineWriteStatus(
        recordId: '',
        operationId: 'operation',
        itemIndex: 0,
        state: OfflineWriteState.locallyCommitted,
        attemptCount: 0,
      ),
      throwsA(isA<OfflineArgumentException>()),
    );
    expect(
      () => OfflineWriteStatus(
        recordId: 'record',
        operationId: 'operation',
        itemIndex: 0,
        state: OfflineWriteState.locallyCommitted,
        attemptCount: -1,
      ),
      throwsA(isA<OfflineArgumentException>()),
    );
    expect(
      () => OfflineWriteStatus(
        recordId: 'record',
        operationId: 'operation',
        itemIndex: 0,
        state: OfflineWriteState.locallyCommitted,
        attemptCount: 0,
        diagnosticCode: '',
      ),
      throwsA(isA<OfflineArgumentException>()),
    );
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
          maxAge: const Duration(days: 1),
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
          maxAge: const Duration(days: 1),
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
          maxAge: const Duration(days: 1),
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
          maxAge: const Duration(days: 1),
          leaseDuration: const Duration(seconds: 1),
          limit: 2,
        ),
      );
      expect(next.single.recordId, 'two');
    },
  );

  test(
    'lease renewal never shortens a deadline after clock rollback',
    () async {
      final store = InMemoryOfflineStore();
      late OfflineOutboxRecord assigned;
      await store.transaction<void>((transaction) {
        assigned = transaction.enqueue(record('lease', 'lease'));
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
                state: OfflineWriteState.locallyCommitted,
                attemptCount: 0,
              ),
            ],
            updatedAt: now,
          ),
        );
      });
      final claimed = await store.transaction((transaction) {
        final result = transaction.claim(
          'p',
          owner: 'owner',
          now: now,
          maxAge: const Duration(days: 1),
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
                attemptCount: 0,
              ),
            ],
            updatedAt: now,
          ),
        );
        return result.single;
      });
      final before = await store.exportSnapshot();

      final renewed = await store.transaction(
        (transaction) => transaction.renewLease(
          'p',
          assigned.recordId,
          owner: 'owner',
          generation: 0,
          now: now.subtract(const Duration(hours: 1)),
          leaseDuration: const Duration(minutes: 1),
        ),
      );

      expect(renewed, isTrue);
      expect(
        (await store.transaction(
          (transaction) => transaction.getOutbox('p', assigned.recordId)!,
        )).leaseUntil,
        claimed.leaseUntil,
      );
      expect(await store.exportSnapshot(), before);
    },
  );

  test('lease deadlines saturate inside the durable time range', () async {
    final nearMaximum = DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 998);
    final maximum = DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 999);
    final store = InMemoryOfflineStore();
    late OfflineOutboxRecord assigned;
    await store.transaction<void>((transaction) {
      assigned = transaction.enqueue(
        OfflineOutboxRecord(
          recordId: 'maximum-lease',
          operationId: 'maximum-lease-operation',
          itemIndex: 0,
          partitionId: 'p',
          intent: OfflinePutVertexIntent(
            Vertex(
              key: 'maximum-lease',
              value: VertexValue.nil(),
              expiration: null,
            ),
          ),
          enqueuedAt: nearMaximum,
          ordinal: 0,
          state: OfflineOutboxState.enqueued,
          attemptCount: 0,
          generation: 0,
        ),
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
              state: OfflineWriteState.locallyCommitted,
              attemptCount: 0,
            ),
          ],
          updatedAt: nearMaximum,
        ),
      );
    });
    final claimed = await store.transaction((transaction) {
      final result = transaction.claim(
        'p',
        owner: 'owner',
        now: nearMaximum,
        maxAge: const Duration(days: 1),
        leaseDuration: const Duration(days: 1),
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
              attemptCount: 0,
            ),
          ],
          updatedAt: nearMaximum,
        ),
      );
      return result.single;
    });
    expect(claimed.leaseUntil, maximum);
    expect(
      await store.transaction(
        (transaction) => transaction.renewLease(
          'p',
          assigned.recordId,
          owner: 'owner',
          generation: 0,
          now: nearMaximum,
          leaseDuration: const Duration(days: 2),
        ),
      ),
      isTrue,
    );
    expect(
      await store.transaction(
        (transaction) => transaction.claim(
          'p',
          owner: 'other',
          now: maximum,
          maxAge: const Duration(days: 1),
          leaseDuration: const Duration(seconds: 1),
          limit: 1,
        ),
      ),
      isEmpty,
    );
    final snapshot = await store.exportSnapshot();
    expect(() => InMemoryOfflineStore.fromSnapshot(snapshot), returnsNormally);
  });

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

  test('cache byte admission reserves later LRU timestamp growth', () async {
    final epoch = DateTime.fromMicrosecondsSinceEpoch(0, isUtc: true);
    final latest = DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 999);
    final cache = OfflineCacheRecord.value(
      partitionId: 'p',
      generation: 0,
      key: const OfflineEntityKey.vertex('reserved-touch'),
      entity: Vertex(
        key: 'reserved-touch',
        value: VertexValue.nil(),
        expiration: null,
      ),
      validatedAt: now,
      lastAccessAt: epoch,
    );
    final admittedBytes =
        utf8.encode(OfflineCodec.encodeCacheRecord(cache)).length + (20 - 3);
    OfflineStoreLimits limits(int bytes) => OfflineStoreLimits(
      maxCacheBytes: bytes,
      maxCacheBytesPerPartition: bytes,
    );
    final store = InMemoryOfflineStore(limits: limits(admittedBytes));
    await store.transaction<void>(
      (transaction) => transaction.putCache('p', cache),
    );
    await store.transaction<void>(
      (transaction) => transaction.touchCache('p', cache.key, latest),
    );
    final snapshot = await store.exportSnapshot();
    expect(
      await InMemoryOfflineStore.fromSnapshot(
        snapshot,
        limits: limits(admittedBytes),
      ).exportSnapshot(),
      snapshot,
    );
    final below = InMemoryOfflineStore(limits: limits(admittedBytes - 1));
    await expectLater(
      below.transaction<void>(
        (transaction) => transaction.putCache('p', cache),
      ),
      throwsA(isA<OfflineCapacityException>()),
    );
  });

  test(
    'outbox reservation preserves exact-limit lifecycle round-trip',
    () async {
      const maxOwnerBytes = 4;
      const maxDiagnosticBytes = 19;
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
      final admittedBytes =
          utf8.encode(OfflineCodec.encodeOutboxRecord(expected)).length +
          ((19 - 1) + (10 - 8) + (3 * (20 - 4))) +
          ((6 * maxOwnerBytes) - 2) +
          ((6 * maxDiagnosticBytes) - 2);
      final limits = OfflineStoreLimits(
        maxOutboxBytes: admittedBytes,
        maxOutboxBytesPerPartition: admittedBytes,
        maxLeaseOwnerBytes: maxOwnerBytes,
        maxDiagnosticCodeBytes: maxDiagnosticBytes,
      );
      final store = InMemoryOfflineStore(limits: limits);
      await store.transaction<void>((transaction) {
        final assigned = transaction.enqueue(record('one', 'a'));
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
                state: OfflineWriteState.locallyCommitted,
                attemptCount: 0,
              ),
            ],
            updatedAt: now,
          ),
        );
      });

      await expectLater(
        store.transaction(
          (transaction) => transaction.claim(
            'p',
            owner: 'owner',
            now: now,
            maxAge: const Duration(days: 1),
            leaseDuration: const Duration(seconds: 30),
            limit: 1,
          ),
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        (await store.transaction(
          (transaction) => transaction.outbox('p').single,
        )).state,
        OfflineOutboxState.enqueued,
      );

      final claimed = await store.transaction((transaction) {
        final claimed = transaction.claim(
          'p',
          owner: 'ownr',
          now: now,
          maxAge: const Duration(days: 1),
          leaseDuration: const Duration(seconds: 30),
          limit: 1,
        );
        transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: claimed.single.operationId,
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: claimed.single.recordId,
                operationId: claimed.single.operationId,
                itemIndex: 0,
                state: OfflineWriteState.sending,
                attemptCount: 0,
              ),
            ],
            updatedAt: now,
          ),
        );
        return claimed;
      });
      await expectLater(
        store.transaction<void>((transaction) {
          transaction.updateOutbox(
            claimed.single.copyWith(
              state: OfflineOutboxState.enqueued,
              attemptCount: 1,
              nextAttemptAt: now.add(const Duration(seconds: 1)),
              clearLeaseOwner: true,
              clearLeaseUntil: true,
              diagnosticCode: '01234567890123456789',
            ),
          );
        }),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        (await store.transaction(
          (transaction) => transaction.outbox('p').single,
        )).state,
        OfflineOutboxState.sending,
      );

      final retryAt = now.add(const Duration(seconds: 1));
      late OfflineOutboxRecord retriable;
      await store.transaction<void>((transaction) {
        retriable = claimed.single.copyWith(
          state: OfflineOutboxState.enqueued,
          attemptCount: 0x7fffffffffffffff,
          nextAttemptAt: retryAt,
          clearLeaseOwner: true,
          clearLeaseUntil: true,
          diagnosticCode: 'retry',
        );
        transaction.updateOutbox(retriable);
        transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: retriable.operationId,
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: retriable.recordId,
                operationId: retriable.operationId,
                itemIndex: 0,
                state: OfflineWriteState.retryScheduled,
                attemptCount: retriable.attemptCount,
                diagnosticCode: retriable.diagnosticCode,
              ),
            ],
            updatedAt: retryAt,
          ),
        );
      });

      final deadLetteredAt = now.add(const Duration(seconds: 2));
      await store.transaction<void>((transaction) {
        final deadLetter = retriable.copyWith(
          state: OfflineOutboxState.deadLetter,
          clearNextAttemptAt: true,
          deadLetteredAt: deadLetteredAt,
          diagnosticCode: 'fatal',
        );
        transaction.updateOutbox(deadLetter);
        transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: deadLetter.operationId,
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: deadLetter.recordId,
                operationId: deadLetter.operationId,
                itemIndex: 0,
                state: OfflineWriteState.deadLetter,
                attemptCount: deadLetter.attemptCount,
                diagnosticCode: deadLetter.diagnosticCode,
              ),
            ],
            updatedAt: deadLetteredAt,
            terminalAt: deadLetteredAt,
          ),
        );
      });

      final snapshot = await store.exportSnapshot();
      final restored = InMemoryOfflineStore.fromSnapshot(
        snapshot,
        limits: limits,
      );
      expect(await restored.exportSnapshot(), snapshot);

      final belowLimit = InMemoryOfflineStore(
        limits: OfflineStoreLimits(
          maxOutboxBytes: admittedBytes - 1,
          maxOutboxBytesPerPartition: admittedBytes - 1,
          maxLeaseOwnerBytes: maxOwnerBytes,
          maxDiagnosticCodeBytes: maxDiagnosticBytes,
        ),
      );
      await expectLater(
        belowLimit.transaction(
          (transaction) => transaction.enqueue(record('one', 'a')),
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
    },
  );

  test('operation byte caps include escaped diagnostics exactly', () async {
    const maxDiagnosticBytes = 128;
    final diagnostic = List<String>.filled(128, '\u0000').join();
    final operation = OfflineOperationRecord(
      partitionId: 'p',
      generation: 0,
      operationId: 'escaped-diagnostic',
      items: <OfflineWriteStatus>[
        OfflineWriteStatus(
          recordId: 'escaped-record',
          operationId: 'escaped-diagnostic',
          itemIndex: 0,
          state: OfflineWriteState.deadLetter,
          attemptCount: 1,
          diagnosticCode: diagnostic,
        ),
      ],
      updatedAt: now,
      terminalAt: now,
    );
    final admissionBase = OfflineOperationRecord(
      partitionId: 'p',
      generation: 0,
      operationId: operation.operationId,
      items: <OfflineWriteStatus>[
        OfflineWriteStatus(
          recordId: 'escaped-record',
          operationId: operation.operationId,
          itemIndex: 0,
          state: OfflineWriteState.locallyCommitted,
          attemptCount: 0,
        ),
      ],
      updatedAt: DateTime.fromMicrosecondsSinceEpoch(0, isUtc: true),
    );
    final exactBytes =
        utf8.encode(OfflineCodec.encodeOperationRecord(admissionBase)).length +
        ((19 - 1) + ((6 * maxDiagnosticBytes) - 2)) +
        ((20 - 3) + (20 - 4));
    OfflineStoreLimits limits(int bytes) => OfflineStoreLimits(
      maxOperationBytes: bytes,
      maxOperationBytesPerPartition: bytes,
      maxDiagnosticCodeBytes: maxDiagnosticBytes,
    );
    final store = InMemoryOfflineStore(limits: limits(exactBytes));
    await store.transaction<void>(
      (transaction) => transaction.putOperation(operation),
    );
    final snapshot = await store.exportSnapshot();
    expect(
      await InMemoryOfflineStore.fromSnapshot(
        snapshot,
        limits: limits(exactBytes),
      ).exportSnapshot(),
      snapshot,
    );
    final below = InMemoryOfflineStore(limits: limits(exactBytes - 1));
    await expectLater(
      below.transaction<void>(
        (transaction) => transaction.putOperation(operation),
      ),
      throwsA(isA<OfflineCapacityException>()),
    );
  });

  test('store rejects diagnostic bounds below SDK-owned codes', () {
    expect(
      () => InMemoryOfflineStore(
        limits: const OfflineStoreLimits(maxDiagnosticCodeBytes: 18),
      ),
      throwsA(isA<OfflineArgumentException>()),
    );
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
      throwsA(isA<OfflineIdentityConflictException>()),
    );
    expect(
      await store.transaction((transaction) => transaction.outbox('p')),
      hasLength(2),
    );
  });

  test('new legacy Add enqueue and schema-v4+ restore fail closed', () async {
    final legacy = OfflineOutboxRecord(
      recordId: 'legacy-add-rejected',
      operationId: 'legacy-add-operation',
      itemIndex: 0,
      partitionId: 'p',
      intent: OfflineAddEdgeIntent(
        Edge(tail: 'a', head: 'b', weight: 1, expiration: null),
        Uint8List.fromList(List<int>.filled(24, 1)),
      ),
      enqueuedAt: now,
      ordinal: 1,
      state: OfflineOutboxState.enqueued,
      attemptCount: 0,
      generation: 0,
    );
    final operation = OfflineOperationRecord(
      partitionId: 'p',
      generation: 0,
      operationId: legacy.operationId,
      items: <OfflineWriteStatus>[
        OfflineWriteStatus(
          recordId: legacy.recordId,
          operationId: legacy.operationId,
          itemIndex: 0,
          state: OfflineWriteState.locallyCommitted,
          attemptCount: 0,
        ),
      ],
      updatedAt: now,
    );
    final store = InMemoryOfflineStore();
    final before = await store.exportSnapshot();
    await expectLater(
      store.transaction<void>((transaction) => transaction.enqueue(legacy)),
      throwsA(isA<OfflineUnsupportedOperationException>()),
    );
    expect(await store.exportSnapshot(), before);

    final encoded =
        jsonDecode(
              encodeLegacySnapshot(
                schema: 3,
                outbox: <OfflineOutboxRecord>[legacy],
                operations: <OfflineOperationRecord>[operation],
              ),
            )
            as Map<String, Object?>;
    for (final schema in <int>[4, InMemoryOfflineStore.snapshotSchemaVersion]) {
      final candidate = jsonDecode(jsonEncode(encoded)) as Map<String, Object?>;
      candidate['schema'] = schema;
      if (schema == InMemoryOfflineStore.snapshotSchemaVersion) {
        final partition =
            (candidate['partitions']! as List<Object?>).single!
                as Map<String, Object?>;
        partition['replayPausedForAuth'] = false;
      }
      expect(
        () => InMemoryOfflineStore.fromSnapshot(jsonEncode(candidate)),
        throwsA(isA<OfflineCodecException>()),
        reason: 'schema v$schema must never migrate Add',
      );
    }
  });

  test('operation and record identity collisions fail atomically', () async {
    final store = InMemoryOfflineStore();
    late OfflineOutboxRecord original;
    await store.transaction<void>((transaction) {
      original = transaction.enqueue(record('record-id', 'original'));
      transaction.putOperation(
        OfflineOperationRecord(
          partitionId: 'p',
          generation: 0,
          operationId: original.operationId,
          items: <OfflineWriteStatus>[
            OfflineWriteStatus(
              recordId: original.recordId,
              operationId: original.operationId,
              itemIndex: 0,
              state: OfflineWriteState.locallyCommitted,
              attemptCount: 0,
            ),
          ],
          updatedAt: now,
        ),
      );
    });

    for (final differentIntent in <bool>[false, true]) {
      await expectLater(
        store.transaction<void>((transaction) {
          transaction.enqueue(
            OfflineOutboxRecord(
              recordId: original.recordId,
              operationId: 'other-$differentIntent',
              itemIndex: 0,
              partitionId: 'p',
              intent: OfflinePutVertexIntent(
                Vertex(
                  key: differentIntent ? 'different' : 'original',
                  value: VertexValue.string(
                    differentIntent ? 'different' : 'original',
                  ),
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
        throwsA(
          isA<OfflineIdentityConflictException>().having(
            (error) => error.kind,
            'kind',
            OfflineIdentityKind.record,
          ),
        ),
      );
    }
    await expectLater(
      store.transaction<void>((transaction) {
        transaction.enqueue(
          OfflineOutboxRecord(
            recordId: 'other-record',
            operationId: original.operationId,
            itemIndex: 0,
            partitionId: 'p',
            intent: OfflinePutVertexIntent(
              Vertex(
                key: 'other',
                value: VertexValue.string('other'),
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
      throwsA(
        isA<OfflineIdentityConflictException>().having(
          (error) => error.kind,
          'kind',
          OfflineIdentityKind.operation,
        ),
      ),
    );

    final after = await store.transaction(
      (transaction) => transaction.outbox('p').single,
    );
    expect(
      OfflineCodec.encodeOutboxRecord(after),
      OfflineCodec.encodeOutboxRecord(original),
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
          maxAge: const Duration(days: 1),
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
            maxAge: const Duration(days: 1),
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
          maxAge: const Duration(days: 1),
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
      () => InMemoryOfflineStore.fromSnapshot('{"schema":6,"partitions":[]}'),
      throwsA(isA<OfflineSchemaException>()),
    );
    expect(
      () => InMemoryOfflineStore.fromSnapshot(
        '{"schema":1,"partitions":[{"partitionId":"p","generation":0,"version":0,"nextOrdinal":0,"cache":["corrupt"],"outbox":[]}]}',
      ),
      throwsA(isA<OfflineCodecException>()),
    );
  });

  test('schema v1 migration rejects sparse allocation amplification', () async {
    final seed = InMemoryOfflineStore();
    await seed.transaction<void>(
      (transaction) => transaction.enqueue(record('sparse', 'sparse')),
    );
    final canonical =
        jsonDecode(await seed.exportSnapshot()) as Map<String, Object?>;

    String sparseSnapshot(int itemIndex) {
      final snapshot =
          jsonDecode(jsonEncode(canonical)) as Map<String, Object?>;
      snapshot['schema'] = 1;
      final partition =
          (snapshot['partitions']! as List<Object?>).single!
              as Map<String, Object?>;
      partition
        ..remove('operations')
        ..remove('replayPausedForAuth');
      final outbox = partition['outbox']! as List<Object?>;
      final encoded =
          jsonDecode(outbox.single! as String) as Map<String, Object?>;
      encoded['itemIndex'] = itemIndex;
      outbox[0] = jsonEncode(encoded);
      return jsonEncode(snapshot);
    }

    for (final itemIndex in <int>[1000000, 0x7fffffffffffffff]) {
      expect(
        () => InMemoryOfflineStore.fromSnapshot(sparseSnapshot(itemIndex)),
        throwsA(isA<OfflineCapacityException>()),
      );
    }
  });

  test('durable counters reject overflow without committing state', () async {
    const maximum = 0x7fffffffffffffff;
    final seed = InMemoryOfflineStore();
    await seed.transaction((transaction) => transaction.generation('p'));
    final canonical =
        jsonDecode(await seed.exportSnapshot()) as Map<String, Object?>;

    InMemoryOfflineStore restoreAt(String field) {
      final snapshot =
          jsonDecode(jsonEncode(canonical)) as Map<String, Object?>;
      final partition =
          (snapshot['partitions']! as List<Object?>).single!
              as Map<String, Object?>;
      partition[field] = maximum;
      return InMemoryOfflineStore.fromSnapshot(jsonEncode(snapshot));
    }

    Future<void> expectAtomicOverflow(
      InMemoryOfflineStore store,
      Future<void> Function() mutate,
    ) async {
      final before = await store.exportSnapshot();
      await expectLater(mutate(), throwsA(isA<OfflineCapacityException>()));
      final after = await store.exportSnapshot();
      expect(after, before);
      expect(() => InMemoryOfflineStore.fromSnapshot(after), returnsNormally);
    }

    final ordinal = restoreAt('nextOrdinal');
    await expectAtomicOverflow(
      ordinal,
      () => ordinal.transaction<void>(
        (transaction) => transaction.enqueue(record('overflow', 'ordinal')),
      ),
    );

    final generation = restoreAt('generation');
    await expectAtomicOverflow(
      generation,
      () => generation.transaction<void>(
        (transaction) => transaction.wipePartition('p'),
      ),
    );

    final version = restoreAt('version');
    await expectAtomicOverflow(
      version,
      () => version.transaction<void>((transaction) {
        transaction.putCache(
          'p',
          OfflineCacheRecord.value(
            partitionId: 'p',
            generation: 0,
            key: const OfflineEntityKey.vertex('version'),
            entity: Vertex(
              key: 'version',
              value: VertexValue.nil(),
              expiration: null,
            ),
            validatedAt: now,
            lastAccessAt: now,
          ),
        );
      }),
    );
  });

  test('snapshot restore rejects contradictory durable state graphs', () async {
    final store = InMemoryOfflineStore();
    await store.transaction<void>((transaction) {
      final assigned = transaction.enqueue(record('graph-record', 'key'));
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
              state: OfflineWriteState.locallyCommitted,
              attemptCount: 0,
            ),
          ],
          updatedAt: now,
        ),
      );
    });
    final canonical =
        jsonDecode(await store.exportSnapshot()) as Map<String, Object?>;

    String corruptOperation(String Function(String) mutate) {
      final snapshot =
          jsonDecode(jsonEncode(canonical)) as Map<String, Object?>;
      final partition =
          (snapshot['partitions']! as List<Object?>).single
              as Map<String, Object?>;
      final operations = partition['operations']! as List<Object?>;
      operations[0] = mutate(operations.single! as String);
      return jsonEncode(snapshot);
    }

    String corruptOutbox(String Function(String) mutate) {
      final snapshot =
          jsonDecode(jsonEncode(canonical)) as Map<String, Object?>;
      final partition =
          (snapshot['partitions']! as List<Object?>).single
              as Map<String, Object?>;
      final outbox = partition['outbox']! as List<Object?>;
      outbox[0] = mutate(outbox.single! as String);
      return jsonEncode(snapshot);
    }

    for (final contradiction in <String>[
      corruptOperation(
        (value) => value.replaceFirst(
          '"recordId":"graph-record"',
          '"recordId":"other-record"',
        ),
      ),
      corruptOperation(
        (value) => value.replaceFirst(
          '"state":"locallyCommitted"',
          '"state":"sending"',
        ),
      ),
      corruptOperation(
        (value) => value.replaceFirst('"generation":0', '"generation":1'),
      ),
      corruptOutbox(
        (value) =>
            value.replaceFirst('"nextAttemptAt":null', '"nextAttemptAt":"0"'),
      ),
      corruptOutbox(
        (value) => value.replaceFirst('"ordinal":1', '"ordinal":0'),
      ),
    ]) {
      expect(
        () => InMemoryOfflineStore.fromSnapshot(contradiction),
        throwsA(isA<OfflineCodecException>()),
      );
    }

    final terminalStore = InMemoryOfflineStore();
    await terminalStore.transaction<void>((transaction) {
      final assigned = transaction.enqueue(
        OfflineOutboxRecord(
          recordId: 'terminal-graph-record',
          operationId: 'terminal-graph-operation',
          itemIndex: 0,
          partitionId: 'p',
          intent: OfflinePutVertexIntent(
            Vertex(
              key: 'terminal',
              value: VertexValue.string('terminal'),
              expiration: null,
            ),
          ),
          enqueuedAt: now,
          ordinal: 0,
          state: OfflineOutboxState.deadLetter,
          attemptCount: 0,
          generation: 0,
          deadLetteredAt: now.add(const Duration(seconds: 2)),
          diagnosticCode: 'terminal',
        ),
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
              state: OfflineWriteState.deadLetter,
              attemptCount: 0,
              diagnosticCode: 'terminal',
            ),
          ],
          updatedAt: now.add(const Duration(seconds: 2)),
          terminalAt: now.add(const Duration(seconds: 2)),
        ),
      );
    });
    final badTerminal =
        jsonDecode(await terminalStore.exportSnapshot())
            as Map<String, Object?>;
    final terminalPartition =
        (badTerminal['partitions']! as List<Object?>).single
            as Map<String, Object?>;
    final terminalOperations =
        terminalPartition['operations']! as List<Object?>;
    final terminalMicros = now
        .add(const Duration(seconds: 2))
        .microsecondsSinceEpoch;
    final earlierMicros = now
        .add(const Duration(seconds: 1))
        .microsecondsSinceEpoch;
    terminalOperations[0] = (terminalOperations.single! as String).replaceFirst(
      '"terminalAt":"$terminalMicros"',
      '"terminalAt":"$earlierMicros"',
    );
    expect(
      () => InMemoryOfflineStore.fromSnapshot(jsonEncode(badTerminal)),
      throwsA(isA<OfflineCodecException>()),
    );

    final ordinal = jsonDecode(jsonEncode(canonical)) as Map<String, Object?>;
    final partition =
        (ordinal['partitions']! as List<Object?>).single
            as Map<String, Object?>;
    partition['nextOrdinal'] = 0;
    expect(
      () => InMemoryOfflineStore.fromSnapshot(jsonEncode(ordinal)),
      throwsA(isA<OfflineCodecException>()),
    );

    final duplicateStore = InMemoryOfflineStore();
    await duplicateStore.transaction<void>((transaction) {
      final assigned = transaction.enqueueAll(<OfflineOutboxRecord>[
        for (var index = 0; index < 2; index++)
          OfflineOutboxRecord(
            recordId: 'duplicate-$index',
            operationId: 'duplicate-operation',
            itemIndex: index,
            partitionId: 'p',
            intent: OfflinePutVertexIntent(
              Vertex(
                key: index == 0 ? 'a' : 'b',
                value: VertexValue.string('value'),
                expiration: null,
              ),
            ),
            enqueuedAt: now,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: 0,
            generation: 0,
          ),
      ]);
      transaction.putOperation(
        OfflineOperationRecord(
          partitionId: 'p',
          generation: 0,
          operationId: assigned.first.operationId,
          items: assigned
              .map(
                (item) => OfflineWriteStatus(
                  recordId: item.recordId,
                  operationId: item.operationId,
                  itemIndex: item.itemIndex,
                  state: OfflineWriteState.locallyCommitted,
                  attemptCount: 0,
                ),
              )
              .toList(growable: false),
          updatedAt: now,
        ),
      );
    });
    final duplicate =
        jsonDecode(await duplicateStore.exportSnapshot())
            as Map<String, Object?>;
    final duplicatePartition =
        (duplicate['partitions']! as List<Object?>).single
            as Map<String, Object?>;
    final duplicateOperations =
        duplicatePartition['operations']! as List<Object?>;
    duplicateOperations[0] = (duplicateOperations.single! as String)
        .replaceFirst('"recordId":"duplicate-1"', '"recordId":"duplicate-0"');
    expect(
      () => InMemoryOfflineStore.fromSnapshot(jsonEncode(duplicate)),
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
      (value! as Map<String, Object?>)
        ..remove('operations')
        ..remove('replayPausedForAuth');
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
    'schema v1 migration allocates globally unique deterministic placeholders',
    () async {
      final store = InMemoryOfflineStore();
      Future<void> enqueueLegacyBatch(
        String operationId,
        String pendingRecordId,
      ) async {
        await store.transaction<void>((transaction) {
          final records = transaction.enqueueAll(<OfflineOutboxRecord>[
            for (var index = 0; index < 2; index++)
              OfflineOutboxRecord(
                recordId: index == 0
                    ? '$operationId-confirmed'
                    : pendingRecordId,
                operationId: operationId,
                itemIndex: index,
                partitionId: 'p',
                intent: OfflinePutVertexIntent(
                  Vertex(
                    key: '$operationId-$index',
                    value: VertexValue.string('value'),
                    expiration: null,
                  ),
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
      }

      await enqueueLegacyBatch('operation-a', 'operation-a-pending');
      await enqueueLegacyBatch('operation-b', 'migrated-unknown-operation-a-0');
      final legacy =
          jsonDecode(await store.exportSnapshot()) as Map<String, Object?>;
      legacy['schema'] = 1;
      for (final value in legacy['partitions']! as List<Object?>) {
        (value! as Map<String, Object?>)
          ..remove('operations')
          ..remove('replayPausedForAuth');
      }
      final encoded = jsonEncode(legacy);

      final first = InMemoryOfflineStore.fromSnapshot(encoded);
      final second = InMemoryOfflineStore.fromSnapshot(encoded);
      final statuses = await first.transaction((transaction) {
        return <OfflineOperationStatus>[
          transaction.getOperation('p', 'operation-a')!.status,
          transaction.getOperation('p', 'operation-b')!.status,
        ];
      });
      final recordIds = statuses
          .expand((operation) => operation.items)
          .map((item) => item.recordId)
          .toList(growable: false);

      expect(recordIds, hasLength(recordIds.toSet().length));
      expect(recordIds, contains('migrated-unknown-operation-a-0'));
      expect(statuses.map((status) => status.items.first.state), <Object?>[
        OfflineWriteState.outcomeUnknown,
        OfflineWriteState.outcomeUnknown,
      ]);
      expect(await first.exportSnapshot(), await second.exportSnapshot());
    },
  );

  test('schema v1-v4 restore recovers durable auth pause', () async {
    final store = InMemoryOfflineStore();
    await store.transaction<void>((transaction) {
      final assigned = transaction.enqueue(
        OfflineOutboxRecord(
          recordId: 'auth-record',
          operationId: 'auth-operation',
          itemIndex: 0,
          partitionId: 'p',
          intent: OfflinePutVertexIntent(
            Vertex(
              key: 'auth-key',
              value: VertexValue.string('value'),
              expiration: null,
            ),
          ),
          enqueuedAt: now,
          ordinal: 0,
          state: OfflineOutboxState.enqueued,
          attemptCount: 0,
          generation: 0,
          diagnosticCode: 'unauthenticated',
        ),
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
              state: OfflineWriteState.pausedForAuth,
              attemptCount: 0,
              diagnosticCode: 'unauthenticated',
            ),
          ],
          updatedAt: now,
        ),
      );
      transaction.setReplayPausedForAuth('p', true);
    });
    final canonical =
        jsonDecode(await store.exportSnapshot()) as Map<String, Object?>;

    for (var schema = 1; schema <= 4; schema++) {
      final legacy = jsonDecode(jsonEncode(canonical)) as Map<String, Object?>;
      legacy['schema'] = schema;
      final partition =
          (legacy['partitions']! as List<Object?>).single!
              as Map<String, Object?>;
      partition.remove('replayPausedForAuth');
      if (schema == 1) partition.remove('operations');

      final restored = InMemoryOfflineStore.fromSnapshot(jsonEncode(legacy));
      final state = await restored.transaction((transaction) {
        return (
          paused: transaction.replayPausedForAuth('p'),
          outbox: transaction.outbox('p').single,
          operation: transaction.getOperation('p', 'auth-operation')!,
        );
      });
      expect(state.paused, isTrue, reason: 'schema v$schema');
      expect(state.outbox.diagnosticCode, 'unauthenticated');
      expect(
        state.operation.items.single.state,
        OfflineWriteState.pausedForAuth,
      );
    }
  });

  test(
    'schema v1 migration quarantines legacy Add while reconstructing status',
    () async {
      final legacyRecord = OfflineOutboxRecord(
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
        ordinal: 1,
        state: OfflineOutboxState.sending,
        attemptCount: 2,
        generation: 0,
        leaseOwner: 'legacy-process',
        leaseUntil: now.add(const Duration(minutes: 1)),
        diagnosticCode: 'unavailable',
      );
      final restored = restoreLegacySnapshot(
        schema: 1,
        outbox: <OfflineOutboxRecord>[legacyRecord],
      );
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
            maxAge: const Duration(days: 1),
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
      final legacyRecord = OfflineOutboxRecord(
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
        ordinal: 1,
        state: OfflineOutboxState.sending,
        attemptCount: 0,
        generation: 0,
        leaseOwner: 'old-process',
        leaseUntil: now.add(const Duration(minutes: 1)),
      );
      final legacyOperation = OfflineOperationRecord(
        partitionId: 'p',
        generation: 0,
        operationId: legacyRecord.operationId,
        items: <OfflineWriteStatus>[
          OfflineWriteStatus(
            recordId: legacyRecord.recordId,
            operationId: legacyRecord.operationId,
            itemIndex: 0,
            state: OfflineWriteState.sending,
            attemptCount: 0,
          ),
        ],
        updatedAt: now,
      );
      final legacy = jsonDecode(
        encodeLegacySnapshot(
          schema: 2,
          outbox: <OfflineOutboxRecord>[legacyRecord],
          operations: <OfflineOperationRecord>[legacyOperation],
        ),
      );

      final overflow = jsonDecode(jsonEncode(legacy)) as Map<String, Object?>;
      final overflowPartition =
          (overflow['partitions']! as List<Object?>).single!
              as Map<String, Object?>;
      overflowPartition['version'] = 0x7fffffffffffffff;
      expect(
        () => InMemoryOfflineStore.fromSnapshot(jsonEncode(overflow)),
        throwsA(isA<OfflineCapacityException>()),
      );

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
            maxAge: const Duration(days: 1),
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
      OfflineOutboxRecord pending(String id) => OfflineOutboxRecord(
        recordId: 'record-$id',
        operationId: id,
        itemIndex: 0,
        partitionId: 'p',
        intent: OfflinePutVertexIntent(
          Vertex(key: id, value: VertexValue.string(id), expiration: null),
        ),
        enqueuedAt: now,
        ordinal: 0,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: 0,
      );
      await store.transaction<void>((transaction) {
        transaction.enqueue(pending('active'));
        transaction.putOperation(
          operation('active', OfflineWriteState.locallyCommitted),
        );
      });
      var caughtCapacity = false;
      await store.transaction<void>((transaction) {
        try {
          transaction.putOperation(
            operation('terminal', OfflineWriteState.confirmed, terminalAt: now),
          );
        } on OfflineCapacityException {
          caughtCapacity = true;
        }
      });
      expect(caughtCapacity, isTrue);
      expect(
        await store.transaction(
          (transaction) => transaction.operations('p').single.operationId,
        ),
        'active',
      );
      final snapshot = await store.exportSnapshot();
      expect(
        await InMemoryOfflineStore.fromSnapshot(
          snapshot,
          limits: const OfflineStoreLimits(
            maxOperationRecords: 1,
            maxOperationRecordsPerPartition: 1,
          ),
        ).transaction(
          (transaction) => transaction.operations('p').single.operationId,
        ),
        'active',
      );
      await expectLater(
        store.transaction<void>((transaction) {
          transaction.enqueue(pending('second'));
          transaction.putOperation(
            operation('second', OfflineWriteState.locallyCommitted),
          );
        }),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        await store.transaction(
          (transaction) => transaction.operations('p').single.operationId,
        ),
        'active',
      );

      await store.transaction<void>((transaction) {
        transaction.putOperation(
          operation('active', OfflineWriteState.confirmed, terminalAt: now),
        );
        transaction.deleteOutbox('p', 'record-active');
      });
      await store.transaction<void>((transaction) {
        transaction.enqueue(pending('second'));
        transaction.putOperation(
          operation('second', OfflineWriteState.locallyCommitted),
        );
      });
      expect(
        await store.transaction(
          (transaction) => transaction.operations('p').single.operationId,
        ),
        'second',
      );
    },
  );

  test(
    'partition change controllers are bounded and released when idle',
    () async {
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(maxChangeControllers: 1),
      );
      final first = store.changes('first').listen((_) {});
      await expectLater(
        store.changes('second').toList(),
        throwsA(isA<OfflineCapacityException>()),
      );
      await first.cancel();
      final second = store.changes('second').listen((_) {});
      await second.cancel();
    },
  );

  test('unlistened change streams consume no controller capacity', () async {
    final store = InMemoryOfflineStore(
      limits: const OfflineStoreLimits(maxChangeControllers: 1),
    );
    for (var index = 0; index < 100; index++) {
      store.changes('unused-$index');
    }
    final active = store.changes('active').listen((_) {});
    await active.cancel();
  });

  test('rapid change-stream relisten still releases capacity', () async {
    final store = InMemoryOfflineStore(
      limits: const OfflineStoreLimits(maxChangeControllers: 1),
    );
    final stream = store.changes('reused');
    final first = stream.listen((_) {});
    final firstCanceled = first.cancel();
    final second = stream.listen((_) {});
    await firstCanceled;
    await second.cancel();
    final other = store.changes('other').listen((_) {});
    await other.cancel();
  });

  test('partition change versions remain monotone across wipe', () async {
    final store = InMemoryOfflineStore();
    final changes = store.changes('p').take(3).toList();
    OfflineCacheRecord cache(String key) => OfflineCacheRecord.value(
      partitionId: 'p',
      generation: 0,
      key: OfflineEntityKey.vertex(key),
      entity: Vertex(key: key, value: VertexValue.nil(), expiration: null),
      validatedAt: now,
      lastAccessAt: now,
    );
    await store.transaction<void>(
      (transaction) => transaction.putCache('p', cache('one')),
    );
    await store.transaction<void>(
      (transaction) => transaction.putCache('p', cache('two')),
    );
    await store.transaction<void>(
      (transaction) => transaction.wipePartition('p'),
    );

    final observed = await changes;
    expect(observed.map((change) => change.version), <int>[1, 2, 3]);
    expect(observed.map((change) => change.generation), <int>[0, 0, 1]);
  });
}
