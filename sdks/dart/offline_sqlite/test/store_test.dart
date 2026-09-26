import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  sqfliteFfiInit();
  final factory = databaseFactoryFfi;
  late Directory directory;
  final stores = <SqliteOfflineStore>[];
  var counter = 0;
  const limits = OfflineStoreLimits(
    maxCacheRecords: 32,
    maxCacheRecordsPerPartition: 32,
    maxOutboxRecords: 32,
    maxOutboxRecordsPerPartition: 32,
    maxOperationRecords: 32,
    maxOperationRecordsPerPartition: 32,
    maxChangeControllers: 8,
  );

  setUp(() async {
    directory = await Directory.systemTemp.createTemp('lantern-sqlite-test-');
  });
  tearDown(() async {
    for (final store in stores.reversed) {
      await store.close();
    }
    stores.clear();
    await directory.delete(recursive: true);
  });
  Future<SqliteOfflineStore> open({
    String? path,
    DatabaseFactory? databaseFactory,
  }) async {
    final store = await SqliteOfflineStore.open(
      path: path ?? '${directory.path}/${counter++}.db',
      databaseFactory: databaseFactory ?? factory,
      limits: limits,
    );
    stores.add(store);
    return store;
  }

  Future<OfflineStore> reopen(OfflineStore input) async {
    final store = input as SqliteOfflineStore;
    final path = store.path;
    await store.close();
    return open(path: path);
  }

  test(
    'complete storage-neutral conformance across real SQLite reopen',
    () async {
      await runStoreConformanceSuite(
        open,
        reopen: reopen,
        maxCapacityProbeRecords: 64,
        maxNotificationControllerProbe: 16,
      );
    },
    timeout: const Timeout(Duration(minutes: 3)),
  );

  test(
    'CDC conformance persists partial chunks and unsigned cursors',
    () async {
      await runChangeStoreConformanceSuite(open, reopen: reopen);
    },
  );

  test(
    'pending Put survives reopen with its absolute TTL and operation status',
    () async {
      var store = await open();
      final now = DateTime.utc(2026, 9, 24);
      var repository = OfflineLanternRepository(
        store: store,
        remote: _NoRemote(),
        config: OfflineConfig(clock: () => now),
      );
      final handle = await repository.putVertex(
        partitionId: 'account',
        operationId: 'save',
        input: VertexInput(
          key: 'note',
          value: VertexValue.string('pending'),
          expiresIn: const Duration(minutes: 30),
        ),
      );
      await repository.dispose();
      store = await reopen(store) as SqliteOfflineStore;
      repository = OfflineLanternRepository(
        store: store,
        remote: _NoRemote(),
        config: OfflineConfig(clock: () => now.add(const Duration(minutes: 5))),
      );
      final snapshot = await repository.readVertex(
        'account',
        'note',
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect(snapshot.hasPendingWrites, isTrue);
      expect(snapshot.value?.expiration, now.add(const Duration(minutes: 30)));
      expect(
        (await repository.getWriteStatus(
          'account',
          handle.operationId,
        ))?.items.single.state,
        OfflineWriteState.locallyCommitted,
      );
      await repository.wipePartition('account');
      await repository.dispose();
      store = await reopen(store) as SqliteOfflineStore;
      expect(await store.transaction((t) => t.outbox('account')), isEmpty);
      expect(await store.transaction((t) => t.generation('account')), 1);
    },
  );

  test(
    'receipt evidence and exact terminal results survive SQLite reopen',
    () async {
      var store = await open();
      final now = DateTime.utc(2026, 9, 24);
      final policy = OfflineReceiptPolicy(
        deploymentEpoch: ReceiptEpoch(_bytes(16, 1)),
        retention: const Duration(hours: 24),
        maxEntries: BigInt.from(1024),
        maxBytes: BigInt.from(1024 * 1024),
        fingerprint: _bytes(32, 4),
      );
      final receipt = OfflineReceiptEvidence(
        operationId: _receiptOperationId(policy.deploymentEpoch),
        groupId: ReceiptGroupId(_bytes(16, 5)),
        endpoint: ReceiptEndpoint(
          nodeId: _bytes(16, 2),
          generation: _bytes(16, 3),
        ),
        mutation: ReceiptMutationKind.edgeDelete,
        policy: policy,
        itemIndex: 0,
        itemCount: 1,
        state: OfflineReceiptReconciliationState.lookupUnknown,
        reconciliationAttemptCount: 1,
      );
      late OfflineOutboxRecord assigned;
      await store.transaction((transaction) async {
        assigned = await transaction.enqueue(
          OfflineOutboxRecord(
            recordId: 'receipt-record',
            operationId: 'receipt-operation',
            itemIndex: 0,
            partitionId: 'p',
            intent: OfflineDeleteEdgeIntent(const EdgeRef('tail', 'head')),
            enqueuedAt: now,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: 1,
            generation: 0,
            receipt: receipt,
            nextAttemptAt: now.add(const Duration(seconds: 1)),
            diagnosticCode: 'receipt_status_unavailable',
          ),
        );
        await transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: 'receipt-operation',
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: assigned.recordId,
                operationId: assigned.operationId,
                itemIndex: 0,
                state: OfflineWriteState.retryScheduled,
                attemptCount: 1,
                diagnosticCode: 'receipt_status_unavailable',
              ),
            ],
            updatedAt: now,
          ),
        );
      });

      store = await reopen(store) as SqliteOfflineStore;
      final restored = await store.transaction(
        (transaction) => transaction.getOutbox('p', 'receipt-record'),
      );
      expect(restored!.receipt!.operationId, receipt.operationId);
      expect(restored.receipt!.groupId, receipt.groupId);
      expect(restored.receipt!.endpoint, receipt.endpoint);
      expect(restored.receipt!.policy.fingerprint, receipt.policy.fingerprint);
      expect(
        restored.receipt!.state,
        OfflineReceiptReconciliationState.lookupUnknown,
      );

      final terminalAt = now.add(const Duration(seconds: 2));
      await store.transaction((transaction) async {
        await transaction.updateOutbox(
          restored.copyWith(
            state: OfflineOutboxState.deadLetter,
            clearNextAttemptAt: true,
            deadLetteredAt: terminalAt,
            receipt: restored.receipt!.copyWith(
              state: OfflineReceiptReconciliationState.noLongerProvable,
              reconciliationAttemptCount: 2,
            ),
            diagnosticCode: 'receipt_no_longer_provable',
          ),
        );
        await transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: 'receipt-operation',
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: restored.recordId,
                operationId: restored.operationId,
                itemIndex: 0,
                state: OfflineWriteState.outcomeUnknown,
                attemptCount: 1,
                diagnosticCode: 'receipt_no_longer_provable',
              ),
            ],
            updatedAt: terminalAt,
            terminalAt: terminalAt,
          ),
        );
        await transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: 'confirmed-receipt-operation',
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: 'confirmed-receipt-record',
                operationId: 'confirmed-receipt-operation',
                itemIndex: 0,
                state: OfflineWriteState.confirmed,
                attemptCount: 1,
                receiptResult: const OfflineEdgeDeleteReceiptResult(false),
              ),
            ],
            updatedAt: terminalAt,
            terminalAt: terminalAt,
          ),
        );
      });
      store = await reopen(store) as SqliteOfflineStore;
      expect(
        (await store.transaction(
          (transaction) => transaction.getOperation('p', 'receipt-operation'),
        ))!.items.single.state,
        OfflineWriteState.outcomeUnknown,
      );
      expect(
        ((await store.transaction(
                  (transaction) => transaction.getOperation(
                    'p',
                    'confirmed-receipt-operation',
                  ),
                ))!.items.single.receiptResult
                as OfflineEdgeDeleteReceiptResult)
            .existed,
        isFalse,
      );
    },
  );

  test('receipt Add intent and exact weight survive SQLite reopen', () async {
    var store = await open();
    final now = DateTime.utc(2026, 9, 24);
    final policy = OfflineReceiptPolicy(
      deploymentEpoch: ReceiptEpoch(_bytes(16, 1)),
      retention: const Duration(hours: 24),
      maxEntries: BigInt.from(1024),
      maxBytes: BigInt.from(1024 * 1024),
      fingerprint: _bytes(32, 4),
    );
    final evidence = OfflineReceiptEvidence(
      operationId: _receiptOperationId(policy.deploymentEpoch),
      groupId: ReceiptGroupId(_bytes(16, 6)),
      endpoint: ReceiptEndpoint(
        nodeId: _bytes(16, 2),
        generation: _bytes(16, 3),
      ),
      mutation: ReceiptMutationKind.edgeAdd,
      policy: policy,
      itemIndex: 0,
      itemCount: 1,
      state: OfflineReceiptReconciliationState.statusRequired,
    );
    late OfflineOutboxRecord assigned;
    await store.transaction((transaction) async {
      assigned = await transaction.enqueue(
        OfflineOutboxRecord(
          recordId: 'add-record',
          operationId: 'add-operation',
          itemIndex: 0,
          partitionId: 'p',
          intent: OfflineReceiptAddEdgeIntent(
            const Edge(tail: 'tail', head: 'head', weight: 3),
            _bytes(24, 7),
          ),
          enqueuedAt: now,
          ordinal: 0,
          state: OfflineOutboxState.enqueued,
          attemptCount: 0,
          generation: 0,
          receipt: evidence,
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
              attemptCount: 0,
            ),
          ],
          updatedAt: now,
        ),
      );
    });

    store = await reopen(store) as SqliteOfflineStore;
    final restored = await store.transaction(
      (transaction) => transaction.getOutbox('p', 'add-record'),
    );
    expect(restored!.receipt!.mutation, ReceiptMutationKind.edgeAdd);
    expect(
      (restored.intent as OfflineReceiptAddEdgeIntent).contributionId,
      _bytes(24, 7),
    );
    final confirmedAt = now.add(const Duration(seconds: 1));
    await store.transaction((transaction) async {
      await transaction.deleteOutbox('p', restored.recordId);
      await transaction.putOperation(
        OfflineOperationRecord(
          partitionId: 'p',
          generation: 0,
          operationId: restored.operationId,
          items: <OfflineWriteStatus>[
            OfflineWriteStatus(
              recordId: restored.recordId,
              operationId: restored.operationId,
              itemIndex: 0,
              state: OfflineWriteState.confirmed,
              attemptCount: 1,
              receiptResult: OfflineEdgeAddReceiptResult(
                double.negativeInfinity,
              ),
            ),
          ],
          updatedAt: confirmedAt,
          terminalAt: confirmedAt,
        ),
      );
    });

    store = await reopen(store) as SqliteOfflineStore;
    final operation = await store.transaction(
      (transaction) => transaction.getOperation('p', 'add-operation'),
    );
    expect(
      (operation!.items.single.receiptResult as OfflineEdgeAddReceiptResult)
          .effectiveWeight,
      double.negativeInfinity,
    );
  });

  test(
    'independent connections serialize writes and publish committed changes',
    () async {
      final first = await open();
      final second = await open(path: first.path);
      final events = <OfflineStoreChange>[];
      final subscription = second.changes('p').listen(events.add);
      await Future.wait([
        first.transaction((t) => t.setReplayPausedForAuth('p', true)),
        second.transaction((t) => t.wipePartition('p')),
      ]);
      await Future<void>.delayed(Duration.zero);
      expect(events.length, 2);
      expect(events.last.version, greaterThan(events.first.version));
      await subscription.cancel();
    },
  );

  test(
    'bounded due scans skip retained dead letters before their retention',
    () async {
      final store = await open();
      final now = DateTime.utc(2026, 9, 24);
      await store.transaction((transaction) async {
        for (final dead in [true, false]) {
          final record = _outboxRecord(
            dead ? 'dead' : 'active',
            now: now,
            dead: dead,
            expired: true,
          );
          final assigned = await transaction.enqueue(record);
          await transaction.putOperation(_operation(assigned, now: now));
        }
      });
      Future<List<OfflineOutboxRecord>> due({
        String? operationId,
        OfflineEntityKey? key,
      }) => store.transaction(
        (transaction) => transaction.dueOutbox(
          'p',
          operationId: operationId,
          key: key,
          now: now,
          maxAge: const Duration(days: 1),
          deadLetterRetention: const Duration(hours: 1),
          limit: 1,
        ),
      );
      expect((await due()).map((record) => record.recordId), ['active']);
      expect(
        (await due(
          key: const OfflineEntityKey.vertex('shared'),
        )).map((record) => record.recordId),
        ['active'],
      );
      expect(await due(operationId: 'dead'), isEmpty);
      final repository = OfflineLanternRepository(
        store: store,
        remote: _NoRemote(),
        config: OfflineConfig(
          clock: () => now,
          maxSweepRecordsPerObservation: 1,
          maxAge: const Duration(days: 1),
          deadLetterRetention: const Duration(hours: 1),
        ),
      );
      addTearDown(repository.dispose);
      expect(await repository.listPending('p'), isEmpty);
      expect(
        (await repository.getWriteStatus('p', 'active'))!.items.single.state,
        OfflineWriteState.expired,
      );
      expect((await repository.listDeadLetters('p')).single.recordId, 'dead');
    },
  );

  test('awaited parallel operations serialize their savepoints', () async {
    var store = await open();
    final events = <OfflineStoreChange>[];
    final subscription = store.changes('p').listen(events.add);
    late OfflineStoreTransaction escaped;
    await store.transaction((transaction) async {
      escaped = transaction;
      await Future.wait([
        for (final key in ['first', 'second'])
          Future<void>.value(transaction.putCache('p', _cacheRecord(key))),
      ]);
    });
    await Future<void>.delayed(Duration.zero);
    expect(events, hasLength(1));
    await subscription.cancel();
    await expectLater(
      Future<void>.sync(() => escaped.putCache('p', _cacheRecord('escaped'))),
      throwsA(isA<OfflineTransactionClosedException>()),
    );
    store = await reopen(store) as SqliteOfflineStore;
    for (final key in ['first', 'second']) {
      expect(
        await store.transaction(
          (transaction) =>
              transaction.getCache('p', OfflineEntityKey.vertex(key)),
        ),
        isNotNull,
      );
    }
    expect(
      await store.transaction(
        (transaction) =>
            transaction.getCache('p', const OfflineEntityKey.vertex('escaped')),
      ),
      isNull,
    );
  });

  test(
    'callback completion with an unawaited auth mutation rolls back',
    () async {
      var store = await open();
      final now = DateTime.utc(2026, 9, 24);
      await store.transaction((transaction) async {
        final record = await transaction.enqueue(
          _outboxRecord('paused', now: now, paused: true),
        );
        await transaction.putOperation(
          _operation(record, now: now, paused: true),
        );
        await transaction.setReplayPausedForAuth('p', true);
      });
      final events = <OfflineStoreChange>[];
      final subscription = store.changes('p').listen(events.add);
      late Future<void> pending;
      await expectLater(
        store.transaction<void>((transaction) {
          pending = Future<void>.value(
            transaction.setReplayPausedForAuth('p', false),
          );
        }),
        throwsA(isA<OfflineTransactionClosedException>()),
      );
      await pending;
      await Future<void>.delayed(Duration.zero);
      expect(events, isEmpty);
      await subscription.cancel();
      store = await reopen(store) as SqliteOfflineStore;
      expect(
        await store.transaction(
          (transaction) => transaction.replayPausedForAuth('p'),
        ),
        isTrue,
      );
      expect(
        (await store.transaction(
          (transaction) => transaction.getOperation('p', 'paused'),
        ))!.items.single.state,
        OfflineWriteState.pausedForAuth,
      );
    },
  );

  test(
    'unawaited enqueue and aggregate cannot escape a finished callback',
    () async {
      var store = await open();
      final record = _outboxRecord('late', now: DateTime.utc(2026, 9, 24));
      late Future<OfflineOutboxRecord> pendingEnqueue;
      late Future<void> pendingOperation;
      final events = <OfflineStoreChange>[];
      final subscription = store.changes('p').listen(events.add);
      await expectLater(
        store.transaction<void>((transaction) {
          pendingEnqueue = Future<OfflineOutboxRecord>.value(
            transaction.enqueue(record),
          );
          pendingOperation = Future<void>.value(
            transaction.putOperation(
              _operation(record, now: record.enqueuedAt),
            ),
          );
        }),
        throwsA(isA<OfflineTransactionClosedException>()),
      );
      await pendingEnqueue;
      await pendingOperation;
      await Future<void>.delayed(Duration.zero);
      expect(events, isEmpty);
      await subscription.cancel();
      store = await reopen(store) as SqliteOfflineStore;
      expect(
        await store.transaction((transaction) => transaction.outbox('p')),
        isEmpty,
      );
      expect(
        await store.transaction((transaction) => transaction.operations('p')),
        isEmpty,
      );
    },
  );

  test(
    'pending operation failures do not replace the callback exception',
    () async {
      var store = await open();
      final callbackError = StateError('callback sentinel');
      late Future<void> pendingObserved;
      await expectLater(
        store.transaction<void>((transaction) async {
          await transaction.putCache('p', _cacheRecord('accepted'));
          pendingObserved =
              Future<void>.value(
                transaction.putCache(
                  'p',
                  _cacheRecord('bad-generation', generation: 1),
                ),
              ).then<void>(
                (_) => fail('the pending operation must fail'),
                onError: (Object error) {
                  expect(error, isA<OfflineArgumentException>());
                },
              );
          throw callbackError;
        }),
        throwsA(same(callbackError)),
      );
      await pendingObserved;
      store = await reopen(store) as SqliteOfflineStore;
      for (final key in ['accepted', 'bad-generation']) {
        expect(
          await store.transaction(
            (transaction) =>
                transaction.getCache('p', OfflineEntityKey.vertex(key)),
          ),
          isNull,
        );
      }
    },
  );

  test(
    'unawaited enqueue failure is drained without losing the callback error',
    () async {
      var store = await open();
      final callbackError = StateError('callback sentinel');
      final done = Completer<void>();
      final unhandled = <Object>[];
      Object? observed;
      runZonedGuarded(() async {
        try {
          await store.transaction<void>((transaction) {
            transaction.enqueue(
              _outboxRecord(
                'invalid-generation',
                now: DateTime.utc(2026, 9, 24),
                generation: 1,
              ),
            );
            throw callbackError;
          });
        } catch (error) {
          observed = error;
        } finally {
          done.complete();
        }
      }, (error, stack) => unhandled.add(error));
      await done.future;
      await Future<void>.delayed(Duration.zero);
      expect(observed, same(callbackError));
      expect(unhandled, isEmpty);
      store = await reopen(store) as SqliteOfflineStore;
      expect(
        await store.transaction((transaction) => transaction.outbox('p')),
        isEmpty,
      );
    },
  );

  for (final scenario
      in <
        ({String name, String prefix, List<String> keys, List<String> retained})
      >[
        (
          name: 'surrogate boundary',
          prefix: '😀'.substring(0, 1),
          keys: ['😀/x', '😁/y', 'other'],
          retained: ['other'],
        ),
        (
          name: 'maximum code unit',
          prefix: '\uffff',
          keys: ['\uffff', '\uffff/x', 'other'],
          retained: ['other'],
        ),
        (
          name: 'empty prefix',
          prefix: '',
          keys: ['first', 'second', 'other'],
          retained: [],
        ),
      ]) {
    test(
      'CDC prefix preserves Dart startsWith semantics at ${scenario.name}',
      () async {
        var store = await open();
        for (final key in scenario.keys) {
          await _cache(store, key, 'value');
        }
        const edgeKey = OfflineEntityKey.edge('tail', 'head');
        await store.transaction(
          (transaction) => transaction.putCache(
            'p',
            OfflineCacheRecord.value(
              partitionId: 'p',
              generation: 0,
              key: edgeKey,
              entity: Edge(
                tail: 'tail',
                head: 'head',
                weight: 1,
                expiration: null,
              ),
              validatedAt: DateTime.utc(2026),
              lastAccessAt: DateTime.utc(2026),
            ),
          ),
        );
        await store.transaction(
          (transaction) => transaction.applyChangeChunk(
            'p',
            OfflineChangeChunk(
              origin: '00000000000000000000000000000001',
              sequence: BigInt.one,
              chunkIndex: 0,
              isLast: true,
              vertexPrefixes: [scenario.prefix],
            ),
          ),
        );
        store = await reopen(store) as SqliteOfflineStore;
        for (final key in scenario.keys) {
          final cached = await store.transaction(
            (transaction) =>
                transaction.getCache('p', OfflineEntityKey.vertex(key)),
          );
          expect(cached != null, scenario.retained.contains(key));
        }
        expect(
          await store.transaction(
            (transaction) => transaction.getCache('p', edgeKey),
          ),
          isNotNull,
        );
      },
    );
  }

  for (final literal in ['ENQUEUED', 'en queued']) {
    test(
      'schema validation rejects altered partial-index literal $literal',
      () async {
        final store = await open();
        final path = store.path;
        await store.close();
        final database = await factory.openDatabase(path);
        final definition =
            (await database.rawQuery(
                  "SELECT sql FROM sqlite_master WHERE name='outbox_expiration'",
                )).single['sql']!
                as String;
        await database.execute('DROP INDEX outbox_expiration');
        await database.execute(
          definition.replaceAll("'enqueued'", "'$literal'"),
        );
        await database.close();
        await expectLater(
          open(path: path),
          throwsA(isA<OfflineSchemaException>()),
        );
      },
    );
  }

  test('closed store rejects new work and closes idempotently', () async {
    final store = await open();
    await store.close();
    await store.close();
    await expectLater(
      store.transaction((t) => t.generation('p')),
      throwsA(isA<OfflineDisposedException>()),
    );
  });

  test('unknown schema fails closed without modifying accepted data', () async {
    final store = await open();
    final path = store.path;
    await store.transaction((t) => t.setReplayPausedForAuth('p', true));
    await store.close();
    final database = await factory.openDatabase(path);
    await database.setVersion(999);
    await database.close();
    await expectLater(open(path: path), throwsA(isA<OfflineSchemaException>()));
    final check = await factory.openDatabase(path);
    expect(await check.getVersion(), 999);
    expect((await check.query('partitions')).single['auth_paused'], 1);
    await check.close();
  });

  test('schema 1 migrates cache and CDC metadata without losing data', () async {
    final store = await open();
    await _cache(store, 'retained', 'value');
    const origin = '00000000000000000000000000000001';
    await store.transaction(
      (transaction) => transaction.applyChangeChunk(
        'p',
        OfflineChangeChunk(
          origin: origin,
          sequence: BigInt.one,
          chunkIndex: 0,
          isLast: true,
        ),
      ),
    );
    final path = store.path;
    await store.close();
    final old = await factory.openDatabase(path);
    await old.execute('DROP TABLE recovery');
    await old.execute('ALTER TABLE partitions DROP COLUMN change_epoch');
    await old.update(
      'store_metadata',
      {'value': 'lantern-offline-1'},
      where: 'key=?',
      whereArgs: ['format'],
    );
    await old.setVersion(1);
    await old.execute('DROP INDEX cache_lru');
    await old.close();

    await expectLater(open(path: path), throwsA(isA<OfflineSchemaException>()));
    final rejected = await factory.openDatabase(path);
    expect(await rejected.getVersion(), 1);
    expect(
      (await rejected.rawQuery(
        'PRAGMA table_info(partitions)',
      )).any((row) => row['name'] == 'change_epoch'),
      isFalse,
    );
    await rejected.execute(
      'CREATE INDEX cache_lru ON cache(accessed_at, partition_id, entity_key)',
    );
    await rejected.close();

    final migrated = await open(path: path);
    expect(await migrated.transaction((t) => t.changeEpoch('p')), 0);
    expect(
      (await migrated.transaction(
        (t) => t.changeCursor('p'),
      )).sequences[origin],
      BigInt.one,
    );
    expect(
      await migrated.transaction(
        (t) => t.getCache('p', const OfflineEntityKey.vertex('retained')),
      ),
      isNotNull,
    );
    await migrated.transaction(
      (t) =>
          t.resetChangeCursor('p', OfflineChangeCursor({origin: BigInt.one})),
    );
    expect(
      await migrated.transaction((t) => t.unknownResidents('p', limit: 1)),
      [const OfflineEntityKey.vertex('retained')],
    );
    final reopened = await reopen(migrated);
    expect(
      await reopened.transaction((t) => t.unknownResidents('p', limit: 1)),
      [const OfflineEntityKey.vertex('retained')],
    );
  });

  test('schema 2 rewrites legacy outbox and operation payloads', () async {
    final store = await open();
    final now = DateTime.utc(2026, 9, 24);
    await store.transaction((transaction) async {
      final assigned = await transaction.enqueue(
        _outboxRecord(
          'legacy-payload',
          now: now,
          expiration: now.add(const Duration(hours: 1)),
        ),
      );
      await transaction.putOperation(_operation(assigned, now: now));
    });
    final path = store.path;
    await store.close();
    final old = await factory.openDatabase(path);
    final outboxRow = (await old.query('outbox')).single;
    final outbox =
        jsonDecode(utf8.decode(outboxRow['payload']! as List<int>))
            as Map<String, Object?>;
    outbox['schema'] = 2;
    outbox.remove('receipt');
    final operationRow = (await old.query('operations')).single;
    final operation =
        jsonDecode(utf8.decode(operationRow['payload']! as List<int>))
            as Map<String, Object?>;
    operation['schema'] = 1;
    for (final item in operation['items']! as List<Object?>) {
      (item! as Map<String, Object?>).remove('receiptResult');
    }
    await old.update('outbox', {
      'payload': Uint8List.fromList(utf8.encode(jsonEncode(outbox))),
    });
    await old.update('operations', {
      'payload': Uint8List.fromList(utf8.encode(jsonEncode(operation))),
    });
    await old.update(
      'store_metadata',
      {'value': 'lantern-offline-2'},
      where: 'key=?',
      whereArgs: ['format'],
    );
    await old.setVersion(2);
    await old.close();

    await expectLater(
      SqliteOfflineStore.open(
        path: path,
        databaseFactory: factory,
        limits: const OfflineStoreLimits(
          maxOutboxBytes: 0,
          maxOutboxBytesPerPartition: 0,
        ),
      ),
      throwsA(isA<OfflineCapacityException>()),
    );
    final rolledBack = await factory.openDatabase(path);
    expect(await rolledBack.getVersion(), 2);
    expect(
      (await rolledBack.query('store_metadata')).single['value'],
      'lantern-offline-2',
    );
    expect(
      utf8.decode(
        (await rolledBack.query('outbox')).single['payload']! as List<int>,
      ),
      contains('"schema":2'),
    );
    await rolledBack.close();

    final migrated = await open(path: path);
    expect(
      (await migrated.transaction(
        (transaction) => transaction.outbox('p'),
      )).single.recordId,
      'legacy-payload',
    );
    final check = await factory.openDatabase(path);
    expect(await check.getVersion(), 3);
    expect(
      (await check.query('store_metadata')).single['value'],
      'lantern-offline-3',
    );
    expect(
      utf8.decode(
        (await check.query('outbox')).single['payload']! as List<int>,
      ),
      contains('"schema":3'),
    );
    expect(
      utf8.decode(
        (await check.query('operations')).single['payload']! as List<int>,
      ),
      contains('"schema":2'),
    );
    await check.close();
  });

  test('corrupt record is rejected before it can reach replay', () async {
    final store = await open();
    await _cache(store, 'retained', 'value');
    final path = store.path;
    await store.close();
    final database = await factory.openDatabase(path);
    await database.rawUpdate("UPDATE cache SET payload=x'0000'");
    await database.close();
    await expectLater(open(path: path), throwsA(isA<OfflineException>()));
  });

  test(
    'real SQLITE_FULL rolls back and leaves the old state reopenable',
    () async {
      var store = await open();
      await _cache(store, 'retained', 'accepted');
      final pendingAt = DateTime.utc(2026, 9, 24);
      await store.transaction((transaction) async {
        final assigned = await transaction.enqueue(
          _outboxRecord(
            'disk-full-pending',
            now: pendingAt,
            expiration: pendingAt.add(const Duration(hours: 1)),
          ),
        );
        await transaction.putOperation(_operation(assigned, now: pendingAt));
      });
      Future<(List<String>, List<String>)> pendingSnapshot(
        SqliteOfflineStore current,
      ) => current.transaction(
        (transaction) async => (
          (await transaction.outbox(
            'p',
          )).map(OfflineCodec.encodeOutboxRecord).toList(),
          (await transaction.operations(
            'p',
          )).map(OfflineCodec.encodeOperationRecord).toList(),
        ),
      );
      final pendingBefore = await pendingSnapshot(store);
      expect(pendingBefore.$1, hasLength(1));
      expect(pendingBefore.$2, hasLength(1));
      final path = store.path;
      await store.close();
      store = await open(path: path, databaseFactory: _FullFactory(factory));
      await expectLater(
        _cache(store, 'oversized', 'x' * (512 * 1024)),
        throwsA(
          isA<SqliteOfflineStoreException>().having(
            (error) => error.code,
            'code',
            'sqlite_full',
          ),
        ),
      );
      await store.close();
      store = await open(path: path);
      final pendingAfter = await pendingSnapshot(store);
      expect(pendingAfter.$1, pendingBefore.$1);
      expect(pendingAfter.$2, pendingBefore.$2);
      final retained = await store.transaction(
        (t) => t.getCache('p', const OfflineEntityKey.vertex('retained')),
      );
      expect((retained?.vertex?.value as StringValue).value, 'accepted');
      expect(
        await store.transaction(
          (t) => t.getCache('p', const OfflineEntityKey.vertex('oversized')),
        ),
        isNull,
      );
    },
  );
}

Future<void> _cache(OfflineStore store, String key, String value) {
  final now = DateTime.utc(2026);
  return store.transaction((t) async {
    await t.putCache(
      'p',
      OfflineCacheRecord.value(
        partitionId: 'p',
        generation: await t.generation('p'),
        key: OfflineEntityKey.vertex(key),
        entity: Vertex(
          key: key,
          value: VertexValue.string(value),
          expiration: null,
        ),
        validatedAt: now,
        lastAccessAt: now,
      ),
    );
  });
}

final class _FullFactory implements DatabaseFactory {
  _FullFactory(this.delegate);
  final DatabaseFactory delegate;
  @override
  Future<Database> openDatabase(
    String path, {
    OpenDatabaseOptions? options,
  }) async {
    final database = await delegate.openDatabase(path, options: options);
    final pages =
        (await database.rawQuery('PRAGMA page_count')).single.values.single
            as int;
    await database.rawQuery('PRAGMA max_page_count = ${pages + 1}');
    return database;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

final class _NoRemote implements OfflineRemote {
  @override
  dynamic noSuchMethod(Invocation invocation) =>
      throw StateError('unexpected remote call');
}

OfflineCacheRecord _cacheRecord(String key, {int generation = 0}) =>
    OfflineCacheRecord.value(
      partitionId: 'p',
      generation: generation,
      key: OfflineEntityKey.vertex(key),
      entity: Vertex(
        key: key,
        value: VertexValue.string('value'),
        expiration: null,
      ),
      validatedAt: DateTime.utc(2026),
      lastAccessAt: DateTime.utc(2026),
    );

OfflineOutboxRecord _outboxRecord(
  String id, {
  required DateTime now,
  bool dead = false,
  bool expired = false,
  bool paused = false,
  DateTime? expiration,
  int generation = 0,
}) => OfflineOutboxRecord(
  recordId: id,
  operationId: id,
  itemIndex: 0,
  partitionId: 'p',
  intent: OfflinePutVertexIntent(
    Vertex(
      key: 'shared',
      value: VertexValue.nil(),
      expiration: expired
          ? now.subtract(Duration(seconds: dead ? 10 : 1))
          : expiration,
    ),
  ),
  enqueuedAt: now.subtract(const Duration(seconds: 20)),
  ordinal: 0,
  state: dead ? OfflineOutboxState.deadLetter : OfflineOutboxState.enqueued,
  attemptCount: 0,
  generation: generation,
  deadLetteredAt: dead ? now.subtract(const Duration(seconds: 2)) : null,
  diagnosticCode: dead
      ? 'invalid_argument'
      : paused
      ? 'unauthenticated'
      : null,
);

OfflineOperationRecord _operation(
  OfflineOutboxRecord record, {
  required DateTime now,
  bool paused = false,
}) {
  final dead = record.state == OfflineOutboxState.deadLetter;
  return OfflineOperationRecord(
    partitionId: record.partitionId,
    generation: record.generation,
    operationId: record.operationId,
    items: [
      OfflineWriteStatus(
        recordId: record.recordId,
        operationId: record.operationId,
        itemIndex: 0,
        state: dead
            ? OfflineWriteState.deadLetter
            : paused
            ? OfflineWriteState.pausedForAuth
            : OfflineWriteState.locallyCommitted,
        attemptCount: record.attemptCount,
        diagnosticCode: record.diagnosticCode,
      ),
    ],
    updatedAt: now,
    terminalAt: dead ? now : null,
  );
}

Uint8List _bytes(int length, int value) =>
    Uint8List.fromList(List<int>.filled(length, value));

ReceiptOperationId _receiptOperationId(ReceiptEpoch epoch) {
  final bytes = Uint8List(49);
  bytes[0] = 1;
  bytes.setRange(1, 17, epoch.bytes);
  var timestamp = DateTime.utc(2026, 9, 24).millisecondsSinceEpoch;
  for (var index = 24; index >= 17; index--) {
    bytes[index] = timestamp & 0xff;
    timestamp >>= 8;
  }
  bytes.fillRange(25, 49, 6);
  return ReceiptOperationId(bytes);
}
