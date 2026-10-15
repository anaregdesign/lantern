import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

final _now = DateTime.utc(2026, 10, 15, 12);
DateTime _clock() => _now;
final _factory = databaseFactoryFfiNoIsolate;
final _policy = SqliteInstallationPolicy(
  identity: 'explicit-adoption-host-fixture',
  version: 1,
  evaluate: (_) async => SqliteInstallationEvidence.verified(
    installationId: 'installation',
    legacyAdoptionVerified: true,
  ),
);
Uint8List _bytes(int n, int v) => Uint8List.fromList(List.filled(n, v));

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  sqfliteFfiInit();
  late Directory root;
  late String path;
  late LanternClient client;
  late LanternSessionBinding binding;
  setUp(() async {
    root = await Directory.systemTemp.createTemp('lantern-adoption-test-');
    path = '${root.path}/db';
    client = LanternClient.connect(
      Uri.parse('http://127.0.0.1:1'),
      allowInsecure: true,
    );
    binding = LanternSessionBinding.application(
      serviceOrigin: client.endpoint,
      securityDomain: 'verified-domain',
      accountIdentity: 'verified-account',
      authorityBinding: 'current',
    );
  });
  tearDown(() async {
    await client.close();
    await root.delete(recursive: true);
  });
  Future<SqliteSessionStartup> open({
    DatabaseFactory? factory,
    String source = 'legacy',
    OfflineStoreLimits limits = const OfflineStoreLimits(),
  }) => SqliteOfflineStore.openSession(
    path: path,
    client: client,
    binding: binding,
    installationPolicy: _policy,
    adoption: SqliteScopeAdoption.verified(
      canonicalPath: SqliteOfflineStore.canonicalAutomaticPath(path),
      sourcePartition: source,
      destination: binding,
    ),
    databaseFactory: factory ?? _factory,
    transferAllowed: false,
    clock: _clock,
    limits: limits,
  );

  test(
    'I2 oldest schema upgrades and adopts original opaque intent atomically',
    () async {
      await _oldestFixture(path);
      final ready = await open() as SqliteSessionReady;
      final result = await _snapshot(path);
      expect(result['version'], 9);
      final p = (result['partitions'] as List).single as Map;
      expect(p['partition_id'], binding.stableIdentity);
      expect(p['auth_paused'], 1);
      expect(p['policy_paused'], 0);
      final row = (result['outbox'] as List).single as Map;
      final record = OfflineCodec.decodeOutboxRecord(
        utf8.decode((row['payload'] as List).cast<int>()),
      );
      expect(record.operationId, 'opaque-v1-operation');
      expect(record.recordId, 'opaque-v1-record');
      expect(record.enqueuedAt, _now);
      expect(record.absoluteExpiration, _now.add(const Duration(hours: 1)));
      expect(
        record.mayHaveDispatched,
        true,
        reason: 'missing legacy dispatch proof stays conservative',
      );
      expect(
        ((record.intent as OfflinePutVertexIntent).vertex.value as StringValue)
            .value,
        'original',
      );
      expect(result['cache'], isEmpty);
      expect(result['recovery'], hasLength(1));
      await ready.session.close();
    },
  );

  for (final authority in [null, 'current', 'previous']) {
    test(
      'I2/I4 schema8 mixed graph adoption preserves intent and $authority pause contract',
      () async {
        await _mixed(path, authority: authority);
        await _asVersion8(path);
        final before = await _snapshot(path);
        expect(before['cdc_scoped'], hasLength(1));
        expect(before['traversals'], hasLength(authority == null ? 0 : 1));
        final ready = await open() as SqliteSessionReady;
        final after = await _snapshot(path);
        final p = (after['partitions'] as List).single as Map;
        final old = (before['partitions'] as List).single as Map;
        for (final key in [
          'generation',
          'next_ordinal',
          'time_high_water',
          'auth_paused',
          'paused_credential_generation',
        ]) {
          expect(p[key], old[key], reason: key);
        }
        expect(p['policy_paused'], authority == 'previous' ? 0 : 1);
        expect(p['authority_binding'], 'current');
        expect(p['partition_id'], binding.stableIdentity);
        for (final table in ['outbox', 'operations']) {
          final original = before[table] as List,
              adopted = after[table] as List;
          expect(adopted.length, original.length);
          for (var i = 0; i < original.length; i++) {
            final a = Map<String, dynamic>.from(adopted[i] as Map);
            final b = Map<String, dynamic>.from(original[i] as Map);
            final ap =
                jsonDecode(
                      utf8.decode((a.remove('payload') as List).cast<int>()),
                    )
                    as Map;
            final bp =
                jsonDecode(
                      utf8.decode((b.remove('payload') as List).cast<int>()),
                    )
                    as Map;
            expect(ap['partitionId'], binding.stableIdentity);
            bp['partitionId'] = binding.stableIdentity;
            expect(ap, bp, reason: '$table payload immutable fields');
            expect(
              a['reserved_bytes'] as int,
              greaterThan(b['reserved_bytes'] as int),
            );
            a.remove('reserved_bytes');
            b.remove('reserved_bytes');
            b['partition_id'] = binding.stableIdentity;
            expect(a, b, reason: '$table indexed immutable fields');
          }
        }
        for (final table in ['operation_items', 'operation_dependencies']) {
          final expected = [
            for (final row in before[table] as List)
              {...(row as Map), 'partition_id': binding.stableIdentity},
          ];
          expect(after[table], expected);
        }
        expect(after['cache'], isEmpty);
        expect(after['cdc_scoped'], isEmpty);
        expect(after['traversals'], isEmpty);
        expect(after['recovery'], hasLength(1));
        expect(after['scope_adoptions'], hasLength(1));
        await ready.session.close();
        client = LanternClient.connect(
          binding.serviceOrigin,
          allowInsecure: true,
        );
        final again = await open() as SqliteSessionReady;
        final reopened = await _snapshot(path);
        for (final table in [
          'outbox',
          'operations',
          'operation_items',
          'operation_dependencies',
          'scope_adoptions',
        ]) {
          expect(
            reopened[table],
            after[table],
            reason: '$table idempotent reopen',
          );
        }
        await again.session.close();
      },
    );
  }

  for (final fault in ['beforeUpgradeCommit', 'afterUpgradeCommit']) {
    test(
      'I3 $fault leaves one complete scope and resolves by ledger',
      () async {
        await _mixed(path);
        await _asVersion8(path);
        final before = await _snapshot(path);
        await expectLater(
          open(factory: _UpgradeFault(fault)),
          throwsStateError,
        );
        final interrupted = await _snapshot(path);
        if (fault == 'beforeUpgradeCommit') {
          expect(interrupted, before);
        } else {
          expect(
            (interrupted['partitions'] as List).single['partition_id'],
            binding.stableIdentity,
          );
          expect(interrupted['scope_adoptions'], hasLength(1));
        }
        final ready = await open() as SqliteSessionReady;
        expect((await _snapshot(path))['scope_adoptions'], hasLength(1));
        await ready.session.close();
      },
    );
  }

  test(
    'I3 destination encoding capacity fails atomically without evicting evidence',
    () async {
      await _mixed(path);
      await _asVersion8(path);
      final before = await _snapshot(path);
      final bytes = (before['outbox'] as List).fold<int>(
        0,
        (sum, row) => sum + (row['reserved_bytes'] as int),
      );
      await expectLater(
        open(
          limits: OfflineStoreLimits(
            maxOutboxBytes: bytes,
            maxOutboxBytesPerPartition: bytes,
          ),
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(await _snapshot(path), before);
    },
  );

  test(
    'I2 current schema adopts before runtime and does not repeat adoption on reopen',
    () async {
      await _mixed(path);
      final ready = await open() as SqliteSessionReady;
      final after = await _snapshot(path);
      await ready.session.close();
      client = LanternClient.connect(
        binding.serviceOrigin,
        allowInsecure: true,
      );
      final again = await open() as SqliteSessionReady;
      final reopened = await _snapshot(path);
      expect(reopened['outbox'], after['outbox']);
      expect(reopened['operations'], after['operations']);
      expect(reopened['scope_adoptions'], after['scope_adoptions']);
      await again.session.close();
    },
  );

  for (final conflict in ['wrong-source', 'target-present', 'malformed']) {
    test('I3 $conflict leaves original schema and graph untouched', () async {
      await _mixed(path);
      if (conflict == 'target-present') {
        final store = await SqliteOfflineStore.open(
          path: path,
          databaseFactory: _factory,
        );
        await store.transaction((tx) => tx.generation(binding.stableIdentity));
        await store.close();
      }
      await _asVersion8(path);
      if (conflict == 'malformed') {
        final db = await _factory.openDatabase(path);
        await db.rawUpdate('UPDATE outbox SET reserved_bytes=1');
        await db.close();
      }
      final before = await _snapshot(path);
      await expectLater(
        open(source: conflict == 'wrong-source' ? 'unknown' : 'legacy'),
        conflict == 'malformed'
            ? throwsA(isA<OfflineCodecException>())
            : throwsA(
                isA<SqliteStartupException>().having(
                  (e) => e.reason,
                  'reason',
                  SqliteStartupFailure.scopeConflict,
                ),
              ),
      );
      expect(await _snapshot(path), before);
    });
  }
}

Future<void> _mixed(String path, {String? authority}) async {
  final store = await SqliteOfflineStore.open(
    path: path,
    databaseFactory: _factory,
  );
  await store.transaction((tx) async {
    if (authority != null) await tx.bindAuthority('legacy', authority);
    await tx.effectiveTime('legacy', _now);
    Future<void> item(
      String id,
      OfflineIntent intent, {
      OfflineReceiptEvidence? receipt,
      bool sent = false,
      bool leased = false,
      bool paused = false,
      List<OfflineDependency> dependencies = const [],
      OperationId? modern,
      String? group,
    }) async {
      final op = modern?.encoded ?? id, rid = modern == null ? id : '$op:0';
      final r = await tx.enqueue(
        OfflineOutboxRecord(
          recordId: rid,
          operationId: op,
          itemIndex: 0,
          partitionId: 'legacy',
          intent: intent,
          enqueuedAt: _now,
          ordinal: 0,
          state: leased
              ? OfflineOutboxState.sending
              : OfflineOutboxState.enqueued,
          attemptCount: sent ? 1 : 0,
          generation: 0,
          receipt: receipt,
          mayHaveDispatched: sent,
          dependencies: dependencies,
          diagnosticCode: paused ? 'unauthenticated' : null,
          leaseOwner: leased ? 'original-lease' : null,
          leaseUntil: leased ? _now.add(const Duration(minutes: 2)) : null,
        ),
      );
      final dead = r.state == OfflineOutboxState.deadLetter;
      await tx.putOperation(
        OfflineOperationRecord(
          partitionId: 'legacy',
          generation: 0,
          operationId: op,
          items: [
            OfflineWriteStatus(
              recordId: rid,
              operationId: op,
              itemIndex: 0,
              state: dead
                  ? OfflineWriteState.deadLetter
                  : leased
                  ? OfflineWriteState.sending
                  : paused
                  ? OfflineWriteState.pausedForAuth
                  : OfflineWriteState.locallyCommitted,
              attemptCount: r.attemptCount,
              mayHaveDispatched: r.mayHaveDispatched,
              diagnosticCode: r.diagnosticCode,
            ),
          ],
          updatedAt: _now,
          terminalAt: dead ? _now : null,
          kind: modern == null ? null : MutationKind.putVertices,
          groupId: group,
          guardUntil: modern == null
              ? null
              : const OperationIdPolicy().retentionDeadline(modern),
        ),
      );
    }

    await item('awaiting', OfflineDeleteVertexIntent('awaiting'));
    await item(
      'receipt-add',
      OfflineReceiptAddEdgeIntent(
        Edge(
          tail: 'tail',
          head: 'head',
          weight: 3,
          expiration: _now.add(const Duration(hours: 2)),
        ),
        _bytes(24, 9),
      ),
      receipt: _receipt(false, mutation: ReceiptMutationKind.edgeAdd),
    );
    await item(
      'prepared',
      OfflineDeleteVertexIntent('prepared'),
      receipt: _receipt(false),
    );
    await item(
      'sent',
      OfflineDeleteVertexIntent('sent'),
      receipt: _receipt(true),
      sent: true,
      leased: true,
    );
    await item(
      'paused',
      OfflinePutVertexIntent(
        Vertex(
          key: 'paused',
          value: VertexValue.string('intent'),
          expiration: _now.add(const Duration(hours: 1)),
        ),
      ),
      paused: true,
    );
    await item(
      'old-add',
      OfflinePutVertexIntent(
        Vertex(key: 'old-add', value: VertexValue.nil(), expiration: null),
      ),
    );
    final a = OperationId(issuedAt: _now, nonce: _bytes(24, 11));
    final b = OperationId(issuedAt: _now, nonce: _bytes(24, 12));
    final g = OperationId(issuedAt: _now, nonce: _bytes(24, 13)).encoded;
    await item(
      'fifo1',
      OfflinePutVertexIntent(
        Vertex(key: 'shared', value: VertexValue.int32(1), expiration: null),
      ),
      modern: a,
      group: g,
    );
    await item(
      'fifo2',
      OfflinePutVertexIntent(
        Vertex(key: 'shared', value: VertexValue.int32(2), expiration: null),
      ),
      modern: b,
      group: g,
      dependencies: [
        OfflineDependency(
          operationId: a.encoded,
          itemIndex: 0,
          predicate: MutationDependencyPredicate.putAppliedAndLive,
        ),
      ],
    );
    for (final state in [
      OfflineWriteState.outcomeUnknown,
      OfflineWriteState.acceptedUndisclosed,
      OfflineWriteState.confirmed,
    ]) {
      final id = 'terminal-${state.name}';
      await tx.putOperation(
        OfflineOperationRecord(
          partitionId: 'legacy',
          generation: 0,
          operationId: id,
          items: [
            OfflineWriteStatus(
              recordId: id,
              operationId: id,
              itemIndex: 0,
              state: state,
              attemptCount: 2,
            ),
          ],
          updatedAt: _now,
          terminalAt: _now,
        ),
      );
    }
    await tx.putOperation(
      OfflineOperationRecord(
        partitionId: 'legacy',
        generation: 0,
        operationId: 'exact',
        items: [
          OfflineWriteStatus(
            recordId: 'exact',
            operationId: 'exact',
            itemIndex: 0,
            state: OfflineWriteState.confirmed,
            attemptCount: 1,
            receiptResult: const OfflineVertexPutReceiptResult(
              PutOutcome.conditionNotMet,
            ),
          ),
        ],
        updatedAt: _now,
        terminalAt: _now,
      ),
    );
    await tx.resetScopedChangeCursor(
      'legacy',
      OfflineScopedChangeCursor(_bytes(16, 42)),
    );
    await tx.putCache(
      'legacy',
      OfflineCacheRecord.value(
        partitionId: 'legacy',
        generation: 0,
        key: const OfflineEntityKey.vertex('cached'),
        entity: Vertex(
          key: 'cached',
          value: VertexValue.nil(),
          expiration: null,
        ),
        validatedAt: _now,
        lastAccessAt: _now,
      ),
    );
    if (authority != null) {
      await tx.putTraversal(
        'legacy',
        OfflineTraversalRecord(
          partitionId: 'legacy',
          generation: 0,
          authorityBinding: authority,
          responderId: 'old-responder',
          ownerTag: '0123456789abcdef0123456789abcdef',
          queryKey: LanternTraversalQuery(
            'cached',
            traversal: const PprOptions(),
          ).canonicalKey,
          validityEpoch: await tx.traversalValidityEpoch('legacy'),
          graph: Graph(
            edges: const [],
            vertices: [
              Vertex(key: 'cached', value: VertexValue.nil(), expiration: null),
            ],
          ),
          observedAt: _now,
          freshUntil: _now.add(const Duration(seconds: 30)),
          historyUntil: _now.add(const Duration(seconds: 60)),
          sourceExpiration: null,
          securityUntil: null,
          lastAccessAt: _now,
        ),
      );
    }
    await tx.setReplayPausedForAuth('legacy', true, credentialGeneration: 7);
    await tx.setReplayPausedForPolicy('legacy', true);
  });
  await store.close();
  await _quarantinedLegacyAdd(path);
}

OfflineReceiptEvidence _receipt(
  bool sent, {
  ReceiptMutationKind mutation = ReceiptMutationKind.vertexDelete,
}) {
  final epoch = ReceiptEpoch(_bytes(16, 1));
  final bytes = Uint8List(49)..[0] = 1;
  bytes.setRange(1, 17, epoch.bytes);
  var millis = _now.millisecondsSinceEpoch;
  for (var i = 24; i >= 17; i--) {
    bytes[i] = millis & 255;
    millis >>= 8;
  }
  bytes.fillRange(25, 49, (sent ? 2 : 3) + mutation.index * 4);
  return OfflineReceiptEvidence(
    operationId: ReceiptOperationId(bytes),
    groupId: ReceiptGroupId(_bytes(16, 4)),
    endpoint: ReceiptEndpoint(nodeId: _bytes(16, 5), generation: _bytes(16, 6)),
    mutation: mutation,
    policy: OfflineReceiptPolicy(
      deploymentEpoch: epoch,
      retention: const Duration(hours: 24),
      maxEntries: BigInt.from(1024),
      maxBytes: BigInt.from(1048576),
      fingerprint: _bytes(32, 8),
    ),
    itemIndex: 0,
    itemCount: 1,
    state: OfflineReceiptReconciliationState.lookupUnknown,
    mayHaveDispatched: sent,
    reconciliationAttemptCount: sent ? 2 : 0,
  );
}

Future<void> _asVersion8(String path) async {
  final db = await _factory.openDatabase(path);
  await db.execute('DROP TABLE scope_adoptions');
  await db.execute('DROP TABLE installation_binding');
  await db.update('store_metadata', {'value': 'lantern-offline-8'});
  await db.setVersion(8);
  await db.close();
}

// Build historical evidence directly; production admission intentionally
// refuses legacy Add. The ordinary store validator verifies this fixture before
// adoption. Its existing lifecycle reservation changes only by intent encoding.
Future<void> _quarantinedLegacyAdd(String path) async {
  final db = await _factory.openDatabase(path);
  final row = (await db.query(
    'outbox',
    where: 'record_id=?',
    whereArgs: ['old-add'],
  )).single;
  final old = OfflineCodec.decodeOutboxRecord(
    utf8.decode(row['payload']! as List<int>),
  );
  final record = OfflineOutboxRecord(
    recordId: old.recordId,
    operationId: old.operationId,
    itemIndex: 0,
    partitionId: 'legacy',
    intent: OfflineAddEdgeIntent(
      Edge(tail: 'tail', head: 'head', weight: 2, expiration: null),
      _bytes(24, 7),
    ),
    enqueuedAt: old.enqueuedAt,
    ordinal: old.ordinal,
    state: OfflineOutboxState.deadLetter,
    attemptCount: 0,
    generation: old.generation,
    mayHaveDispatched: false,
    deadLetteredAt: _now,
    diagnosticCode: 'unsupported_add',
  );
  final base = record.copyWith(
    state: OfflineOutboxState.enqueued,
    clearDeadLetteredAt: true,
    clearDiagnosticCode: true,
  );
  final growth =
      utf8.encode(OfflineCodec.encodeOutboxRecord(base)).length -
      utf8.encode(OfflineCodec.encodeOutboxRecord(old)).length;
  final operation = OfflineOperationRecord(
    partitionId: 'legacy',
    generation: old.generation,
    operationId: old.operationId,
    items: [
      OfflineWriteStatus(
        recordId: old.recordId,
        operationId: old.operationId,
        itemIndex: 0,
        state: OfflineWriteState.deadLetter,
        attemptCount: 0,
        mayHaveDispatched: false,
        diagnosticCode: 'unsupported_add',
      ),
    ],
    updatedAt: _now,
    terminalAt: _now,
  );
  await db.update(
    'outbox',
    {
      'entity_key': record.intent.key.canonical,
      'state': 'deadLetter',
      'dead_lettered_at': _now.microsecondsSinceEpoch,
      'reserved_bytes': (row['reserved_bytes']! as int) + growth,
      'payload': Uint8List.fromList(
        utf8.encode(OfflineCodec.encodeOutboxRecord(record)),
      ),
    },
    where: 'record_id=?',
    whereArgs: ['old-add'],
  );
  await db.update(
    'operations',
    {
      'terminal_at': _now.microsecondsSinceEpoch,
      'payload': Uint8List.fromList(
        utf8.encode(OfflineCodec.encodeOperationRecord(operation)),
      ),
    },
    where: 'operation_id=?',
    whereArgs: ['old-add'],
  );
  await db.close();
  await (await SqliteOfflineStore.open(
    path: path,
    databaseFactory: _factory,
  )).close();
}

Future<Map<String, Object?>> _snapshot(String path) async {
  final db = await _factory.openDatabase(
    path,
    options: OpenDatabaseOptions(readOnly: true, singleInstance: false),
  );
  try {
    final result = <String, Object?>{'version': await db.getVersion()};
    for (final table in [
      'partitions',
      'outbox',
      'operations',
      'operation_items',
      'operation_dependencies',
      'cache',
      'recovery',
      'cdc_scoped',
      'traversals',
      'scope_adoptions',
    ]) {
      if ((await db.rawQuery('SELECT name FROM sqlite_master WHERE name=?', [
        table,
      ])).isNotEmpty) {
        result[table] = await db.rawQuery('SELECT * FROM $table ORDER BY 1,2');
      }
    }
    return result;
  } finally {
    await db.close();
  }
}

final class _UpgradeFault implements DatabaseFactory {
  _UpgradeFault(this.fault);
  final String fault;
  @override
  Future<Database> openDatabase(
    String path, {
    OpenDatabaseOptions? options,
  }) async {
    if (options?.readOnly == true) {
      return _factory.openDatabase(path, options: options);
    }
    final o = options!;
    final db = await _factory.openDatabase(
      path,
      options: OpenDatabaseOptions(
        version: o.version,
        singleInstance: o.singleInstance,
        onConfigure: o.onConfigure,
        onCreate: o.onCreate,
        onDowngrade: o.onDowngrade,
        onUpgrade: (db, from, to) async {
          await o.onUpgrade!(db, from, to);
          if (fault == 'beforeUpgradeCommit') {
            throw StateError('injected before upgrade commit');
          }
        },
      ),
    );
    if (fault == 'afterUpgradeCommit') {
      throw StateError('injected lost commit reply');
    }
    return db;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

Future<void> _oldestFixture(String path) async {
  final store = await SqliteOfflineStore.open(
    path: path,
    databaseFactory: _factory,
  );
  await store.transaction((tx) async {
    final r = await tx.enqueue(
      OfflineOutboxRecord(
        recordId: 'opaque-v1-record',
        operationId: 'opaque-v1-operation',
        itemIndex: 0,
        partitionId: 'legacy',
        intent: OfflinePutVertexIntent(
          Vertex(
            key: 'v1',
            value: VertexValue.string('original'),
            expiration: _now.add(const Duration(hours: 1)),
          ),
        ),
        enqueuedAt: _now,
        ordinal: 0,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: 0,
      ),
    );
    await tx.putOperation(
      OfflineOperationRecord(
        partitionId: 'legacy',
        generation: 0,
        operationId: r.operationId,
        items: [
          OfflineWriteStatus(
            recordId: r.recordId,
            operationId: r.operationId,
            itemIndex: 0,
            state: OfflineWriteState.locallyCommitted,
            attemptCount: 0,
          ),
        ],
        updatedAt: _now,
      ),
    );
    await tx.putCache(
      'legacy',
      OfflineCacheRecord.value(
        partitionId: 'legacy',
        generation: 0,
        key: const OfflineEntityKey.vertex('cached'),
        entity: Vertex(
          key: 'cached',
          value: VertexValue.nil(),
          expiration: null,
        ),
        validatedAt: _now,
        lastAccessAt: _now,
      ),
    );
    await tx.setReplayPausedForAuth('legacy', true);
  });
  await store.close();
  final db = await _factory.openDatabase(path);
  for (final table in ['outbox', 'operations']) {
    final row = (await db.query(table)).single;
    final payload =
        jsonDecode(utf8.decode(row['payload']! as List<int>))
            as Map<String, dynamic>;
    payload['schema'] = 1;
    for (final key
        in table == 'outbox'
            ? [
                'deadLetteredAt',
                'preparation',
                'receipt',
                'dependencies',
                'mayHaveDispatched',
              ]
            : ['kind', 'guardUntil', 'groupId']) {
      payload.remove(key);
    }
    if (table == 'operations') {
      for (final item in payload['items'] as List) {
        (item as Map).remove('receiptResult');
        item.remove('mayHaveDispatched');
      }
    }
    await db.update(table, {
      'payload': Uint8List.fromList(utf8.encode(jsonEncode(payload))),
    });
  }
  for (final table in [
    'scope_adoptions',
    'installation_binding',
    'traversals',
    'automatic_owner',
    'operation_dependencies',
    'recovery',
    'cdc_scoped',
  ]) {
    await db.execute('DROP TABLE $table');
  }
  for (final column in [
    'traversal_epoch',
    'paused_credential_generation',
    'policy_paused',
    'time_high_water',
    'authority_binding',
    'change_epoch',
  ]) {
    await db.execute('ALTER TABLE partitions DROP COLUMN $column');
  }
  await db.execute('ALTER TABLE operations DROP COLUMN guard_until');
  await db.update('store_metadata', {'value': 'lantern-offline-1'});
  await db.setVersion(1);
  await db.close();
}
