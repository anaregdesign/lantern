part of '../lantern_client_offline_sqlite.dart';

final class _SqlTransaction implements OfflineStoreTransaction {
  _SqlTransaction(this.sql, this.limits);
  final DatabaseExecutor sql;
  final OfflineStoreLimits limits;
  final Set<String> changed = {};
  final Set<String> mutated = {};
  final Map<String, Map<String, List<OfflineOutboxRecord>>> enqueued = {};
  bool _sealed = false;
  int _savepoint = 0;
  static final _operationZone = Object();
  Future<void> _operationTail = Future<void>.value();
  int _pending = 0;

  Future<void> finish({bool rejectPending = true}) async {
    final unfinished = _pending != 0;
    _sealed = true;
    await _operationTail;
    if (unfinished && rejectPending) {
      throw const OfflineTransactionClosedException();
    }
  }

  void _ensureOpen() {
    if (_sealed && !identical(Zone.current[_operationZone], this)) {
      throw const OfflineTransactionClosedException();
    }
  }

  // Public operations share one queue so concurrent calls cannot interleave
  // savepoints. Nested calls stay in the current operation to avoid deadlock.
  Future<T> _run<T>(Future<T> Function() action) {
    if (identical(Zone.current[_operationZone], this)) {
      return _databaseCall(action);
    }
    if (_sealed) {
      return Future<T>.error(const OfflineTransactionClosedException());
    }
    _pending++;
    final result = _operationTail
        .then(
          (_) => runZoned(
            () => _databaseCall(action),
            zoneValues: {_operationZone: this},
          ),
        )
        .whenComplete(() => _pending--);
    // Observe failures even when a caller violates the await contract. The
    // original future still reports its error to any caller that does await.
    _operationTail = result.then<void>(
      (_) {},
      onError: (Object _, StackTrace _) {},
    );
    return result;
  }

  Future<Map<String, Object?>> _partition(String id) => _databaseCall(() async {
    _ensureOpen();
    _validatePartition(id);
    await sql.rawInsert(
      'INSERT OR IGNORE INTO partitions(partition_id,generation,next_ordinal,version,auth_paused,change_epoch) VALUES (?,0,0,0,0,0)',
      [id],
    );
    return (await sql.query(
      'partitions',
      where: 'partition_id=?',
      whereArgs: [id],
    )).single;
  });

  Future<T> _atomic<T>(Future<T> Function() action) => _run(() async {
    _ensureOpen();
    final name = 'mutation_${_savepoint++}';
    final previousChanges = Set<String>.of(changed);
    final previousMutations = Set<String>.of(mutated);
    final previousEnqueued = enqueued.map(
      (key, value) =>
          MapEntry(key, Map<String, List<OfflineOutboxRecord>>.of(value)),
    );
    await sql.execute('SAVEPOINT $name');
    try {
      final value = await action();
      await sql.execute('RELEASE SAVEPOINT $name');
      return value;
    } catch (error, stack) {
      // Disk errors may already have rolled back the native transaction.
      // A cleanup failure must not hide the original classification.
      try {
        await sql.execute('ROLLBACK TO SAVEPOINT $name');
        await sql.execute('RELEASE SAVEPOINT $name');
      } on DatabaseException {
        // The outer SQLite transaction remains responsible for rollback.
      }
      changed
        ..clear()
        ..addAll(previousChanges);
      mutated
        ..clear()
        ..addAll(previousMutations);
      enqueued
        ..clear()
        ..addAll(previousEnqueued);
      Error.throwWithStackTrace(_safeDatabaseError(error), stack);
    }
  });

  @override
  Future<int> generation(String partitionId) =>
      _run(() async => (await _partition(partitionId))['generation']! as int);

  @override
  Future<bool> replayPausedForAuth(String partitionId) =>
      _run(() async => (await _partition(partitionId))['auth_paused'] == 1);

  @override
  Future<void> setReplayPausedForAuth(String partitionId, bool paused) =>
      _run(() async {
        final metadata = await _partition(partitionId);
        if (metadata['auth_paused'] == (paused ? 1 : 0)) return;
        await sql.update(
          'partitions',
          {'auth_paused': paused ? 1 : 0},
          where: 'partition_id=?',
          whereArgs: [partitionId],
        );
        changed.add(partitionId);
      });

  @override
  Future<OfflineCacheRecord?> getCache(
    String partitionId,
    OfflineEntityKey key,
  ) => _run(() async {
    _ensureOpen();
    _validatePartition(partitionId);
    if ((await sql.rawQuery(
      'SELECT 1 FROM recovery WHERE partition_id=? AND entity_key=? LIMIT 1',
      [partitionId, key.canonical],
    )).isNotEmpty) {
      return null;
    }
    final rows = await sql.query(
      'cache',
      where: 'partition_id=? AND entity_key=?',
      whereArgs: [partitionId, key.canonical],
    );
    return rows.isEmpty ? null : _cache(rows.single);
  });

  @override
  Future<void> putCache(String partitionId, OfflineCacheRecord record) =>
      _atomic(() async {
        final metadata = await _partition(partitionId);
        if (record.partitionId != partitionId ||
            record.generation != metadata['generation']) {
          throw const OfflineArgumentException();
        }
        final bytes = _cacheAdmissionBytesFor(record);
        if (bytes > limits.maxCacheBytes ||
            bytes > limits.maxCacheBytesPerPartition ||
            limits.maxCacheRecords == 0 ||
            limits.maxCacheRecordsPerPartition == 0) {
          throw const OfflineCapacityException();
        }
        await sql.insert(
          'cache',
          _cacheColumns(record, bytes),
          conflictAlgorithm: ConflictAlgorithm.replace,
        );
        await _evictCache(partitionId, record.key.canonical);
        changed.add(partitionId);
      });

  @override
  Future<void> deleteCache(String partitionId, OfflineEntityKey key) =>
      _run(() async {
        _ensureOpen();
        _validatePartition(partitionId);
        if (await sql.delete(
              'cache',
              where: 'partition_id=? AND entity_key=?',
              whereArgs: [partitionId, key.canonical],
            ) !=
            0) {
          changed.add(partitionId);
        }
      });

  @override
  Future<void> touchCache(
    String partitionId,
    OfflineEntityKey key,
    DateTime accessedAt,
  ) => _run(() async {
    final record = await getCache(partitionId, key);
    if (record == null) return;
    final next = record.accessedAt(accessedAt);
    await sql.update(
      'cache',
      _cacheColumns(next, _cacheAdmissionBytesFor(next)),
      where: 'partition_id=? AND entity_key=?',
      whereArgs: [partitionId, key.canonical],
    );
    mutated.add(partitionId);
  });

  @override
  Future<OfflineOutboxRecord?> getOutbox(String partitionId, String recordId) =>
      _run(() async {
        _ensureOpen();
        _validatePartition(partitionId);
        final rows = await sql.query(
          'outbox',
          where: 'partition_id=? AND record_id=?',
          whereArgs: [partitionId, recordId],
        );
        return rows.isEmpty ? null : _outbox(rows.single);
      });

  @override
  Future<List<OfflineOutboxRecord>> outbox(String partitionId) =>
      _run(() async {
        _ensureOpen();
        _validatePartition(partitionId);
        return (await sql.query(
          'outbox',
          where: 'partition_id=?',
          whereArgs: [partitionId],
          orderBy: _fifo,
        )).map(_outbox).toList(growable: false);
      });

  @override
  Future<List<OfflineOutboxRecord>> outboxForKey(
    String partitionId,
    OfflineEntityKey key,
  ) => _run(() async {
    _ensureOpen();
    _validatePartition(partitionId);
    return (await sql.query(
      'outbox',
      where: 'partition_id=? AND entity_key=?',
      whereArgs: [partitionId, key.canonical],
      orderBy: _fifo,
    )).map(_outbox).toList(growable: false);
  });

  ({String where, List<Object?> args}) _scope(
    String partitionId,
    String? operationId,
    OfflineEntityKey? key,
  ) {
    _ensureOpen();
    _validatePartition(partitionId);
    if ((operationId != null && key != null) || operationId?.isEmpty == true) {
      throw const OfflineArgumentException();
    }
    return (
      where:
          'partition_id=?${operationId != null
              ? ' AND operation_id=?'
              : key != null
              ? ' AND entity_key=?'
              : ''}',
      args: [partitionId, ?operationId, if (key != null) key.canonical],
    );
  }

  @override
  Future<OfflineOutboxScanPage> scanOutbox(
    String partitionId, {
    OfflineOutboxCursor? after,
    String? operationId,
    OfflineEntityKey? key,
    required int limit,
  }) => _run(() async {
    final scope = _scope(partitionId, operationId, key);
    if (limit < 1) throw const OfflineArgumentException();
    final afterWhere = after == null ? '' : ' AND $_afterFifo';
    final rows = await sql.query(
      'outbox',
      where: scope.where + afterWhere,
      whereArgs: [
        ...scope.args,
        if (after != null)
          ..._afterFifoArgs(after.ordinal, after.itemIndex, after.recordId),
      ],
      orderBy: _fifo,
      limit: limit,
    );
    final records = rows.map(_outbox).toList(growable: false);
    final last = records.isEmpty ? null : records.last;
    final hasMore =
        last != null &&
        (await sql.rawQuery(
          'SELECT 1 FROM outbox WHERE ${scope.where} AND $_afterFifo LIMIT 1',
          [
            ...scope.args,
            ..._afterFifoArgs(last.ordinal, last.itemIndex, last.recordId),
          ],
        )).isNotEmpty;
    return OfflineOutboxScanPage(
      records: records,
      nextCursor: last == null
          ? null
          : OfflineOutboxCursor(
              ordinal: last.ordinal,
              itemIndex: last.itemIndex,
              recordId: last.recordId,
            ),
      hasMore: hasMore,
    );
  });

  @override
  Future<bool> hasOutboxForOperation(String partitionId, String operationId) =>
      _run(() async {
        final scope = _scope(partitionId, operationId, null);
        return (await sql.rawQuery(
          'SELECT 1 FROM outbox WHERE ${scope.where} LIMIT 1',
          scope.args,
        )).isNotEmpty;
      });

  @override
  Future<List<OfflineOutboxRecord>> dueOutbox(
    String partitionId, {
    String? operationId,
    OfflineEntityKey? key,
    required DateTime now,
    required Duration maxAge,
    required Duration deadLetterRetention,
    required int limit,
  }) => _run(() async {
    final scope = _scope(partitionId, operationId, key);
    if (!now.isUtc ||
        maxAge <= Duration.zero ||
        deadLetterRetention <= Duration.zero ||
        limit < 1) {
      throw const OfflineArgumentException();
    }
    final selected = <String, OfflineOutboxRecord>{};
    var inspected = 0;
    for (final deadline in <(String, int)>[
      ('expiration', now.microsecondsSinceEpoch),
      ('enqueued_at', now.microsecondsSinceEpoch - maxAge.inMicroseconds),
      (
        'dead_lettered_at',
        now.microsecondsSinceEpoch - deadLetterRetention.inMicroseconds,
      ),
    ]) {
      if (inspected >= limit) break;
      final rows = await sql.query(
        'outbox',
        where:
            '${scope.where} AND ${_deadlineState(deadline.$1)} AND ${deadline.$1} <= ?',
        whereArgs: [...scope.args, deadline.$2],
        orderBy: '${deadline.$1}, $_fifo',
        limit: limit - inspected,
      );
      inspected += rows.length;
      for (final row in rows) {
        final record = _outbox(row);
        selected.putIfAbsent(record.recordId, () => record);
      }
    }
    return List<OfflineOutboxRecord>.unmodifiable(selected.values);
  });

  @override
  Future<OfflineOutboxRecord> enqueue(OfflineOutboxRecord record) =>
      _run(() async => (await enqueueAll([record])).single);

  @override
  Future<List<OfflineOutboxRecord>> enqueueAll(
    List<OfflineOutboxRecord> records,
  ) => _atomic(() async {
    if (records.isEmpty) throw const OfflineArgumentException();
    final id = records.first.partitionId;
    final operationId = records.first.operationId;
    final metadata = await _partition(id);
    if (await getOperation(id, operationId) != null ||
        await hasOutboxForOperation(id, operationId) ||
        (enqueued[id]?.containsKey(operationId) ?? false)) {
      throw const OfflineIdentityConflictException(
        OfflineIdentityKind.operation,
      );
    }
    final ids = <String>{};
    final pendingIds =
        enqueued[id]?.values
            .expand((rows) => rows)
            .map((record) => record.recordId)
            .toSet() ??
        <String>{};
    for (var i = 0; i < records.length; i++) {
      final record = records[i];
      if (record.intent is OfflineAddEdgeIntent) {
        throw const OfflineUnsupportedOperationException();
      }
      if (!ids.add(record.recordId) ||
          pendingIds.contains(record.recordId) ||
          (await sql.rawQuery(
            'SELECT 1 FROM outbox WHERE partition_id=? AND record_id=? UNION ALL SELECT 1 FROM operation_items WHERE partition_id=? AND record_id=? LIMIT 1',
            [id, record.recordId, id, record.recordId],
          )).isNotEmpty) {
        throw const OfflineIdentityConflictException(
          OfflineIdentityKind.record,
        );
      }
      if (record.partitionId != id ||
          record.operationId != operationId ||
          record.itemIndex != i ||
          record.generation != metadata['generation']) {
        throw const OfflineArgumentException();
      }
      _validateOutboxLifecycleCapacity(record, limits);
    }
    final ordinal = _checkedIncrement(metadata['next_ordinal']! as int);
    final assigned = records
        .map(
          (record) => OfflineOutboxRecord(
            recordId: record.recordId,
            operationId: record.operationId,
            itemIndex: record.itemIndex,
            partitionId: id,
            intent: record.intent,
            enqueuedAt: record.enqueuedAt,
            ordinal: ordinal,
            state: record.state,
            attemptCount: record.attemptCount,
            generation: record.generation,
            receipt: record.receipt,
            nextAttemptAt: record.nextAttemptAt,
            leaseOwner: record.leaseOwner,
            leaseUntil: record.leaseUntil,
            deadLetteredAt: record.deadLetteredAt,
            diagnosticCode: record.diagnosticCode,
          ),
        )
        .toList(growable: false);
    for (final record in assigned) {
      if (record.state != OfflineOutboxState.expired) {
        await sql.insert('outbox', _outboxColumns(record, limits));
      }
    }
    if (await _over(
          'outbox',
          id,
          limits.maxOutboxRecordsPerPartition,
          limits.maxOutboxBytesPerPartition,
        ) ||
        await _over(
          'outbox',
          null,
          limits.maxOutboxRecords,
          limits.maxOutboxBytes,
        )) {
      throw const OfflineCapacityException();
    }
    await sql.update(
      'partitions',
      {'next_ordinal': ordinal},
      where: 'partition_id=?',
      whereArgs: [id],
    );
    (enqueued[id] ??= {})[operationId] = assigned;
    changed.add(id);
    return assigned
        .map(
          (record) => OfflineCodec.decodeOutboxRecord(
            OfflineCodec.encodeOutboxRecord(record),
          ),
        )
        .toList(growable: false);
  });

  @override
  Future<void> updateOutbox(OfflineOutboxRecord record) => _run(() async {
    final previous = await getOutbox(record.partitionId, record.recordId);
    if (previous == null ||
        !OfflineCodec.sameOutboxIdentity(previous, record)) {
      throw const OfflineArgumentException();
    }
    _validateOutboxLifecycleCapacity(record, limits);
    await sql.update(
      'outbox',
      _outboxColumns(record, limits),
      where: 'partition_id=? AND record_id=?',
      whereArgs: [record.partitionId, record.recordId],
    );
    changed.add(record.partitionId);
  });

  @override
  Future<void> deleteOutbox(String partitionId, String recordId) =>
      _run(() async {
        final record = await getOutbox(partitionId, recordId);
        if (record == null) return;
        final operation = await getOperation(partitionId, record.operationId);
        if (operation != null &&
            !_terminalWriteState(operation.items[record.itemIndex].state)) {
          throw const OfflineArgumentException();
        }
        await sql.delete(
          'outbox',
          where: 'partition_id=? AND record_id=?',
          whereArgs: [partitionId, recordId],
        );
        changed.add(partitionId);
      });

  @override
  Future<OfflineOperationRecord?> getOperation(
    String partitionId,
    String operationId,
  ) => _run(() async {
    _ensureOpen();
    _validatePartition(partitionId);
    final rows = await sql.query(
      'operations',
      where: 'partition_id=? AND operation_id=?',
      whereArgs: [partitionId, operationId],
    );
    return rows.isEmpty ? null : _operation(rows.single);
  });

  @override
  Future<List<OfflineOperationRecord>> operations(String partitionId) =>
      _run(() async {
        _ensureOpen();
        _validatePartition(partitionId);
        return (await sql.query(
          'operations',
          where: 'partition_id=?',
          whereArgs: [partitionId],
          orderBy: 'updated_at, operation_id',
        )).map(_operation).toList(growable: false);
      });

  @override
  Future<OfflineOperationScanPage> scanOperations(
    String partitionId, {
    String? afterOperationId,
    required int limit,
  }) => _run(() async {
    _ensureOpen();
    _validatePartition(partitionId);
    if (limit < 1 || afterOperationId?.isEmpty == true) {
      throw const OfflineArgumentException();
    }
    final rows = await sql.query(
      'operations',
      where:
          'partition_id=?${afterOperationId == null ? '' : ' AND operation_id>?'}',
      whereArgs: [partitionId, ?afterOperationId],
      orderBy: 'operation_id',
      limit: limit,
    );
    final records = rows.map(_operation).toList(growable: false);
    final cursor = records.isEmpty ? null : records.last.operationId;
    final more =
        cursor != null &&
        (await sql.rawQuery(
          'SELECT 1 FROM operations WHERE partition_id=? AND operation_id>? LIMIT 1',
          [partitionId, cursor],
        )).isNotEmpty;
    return OfflineOperationScanPage(
      operations: records,
      nextOperationId: cursor,
      hasMore: more,
    );
  });

  @override
  Future<List<OfflineOperationRecord>> dueOperations(
    String partitionId, {
    required DateTime now,
    required Duration retention,
    required int limit,
  }) => _run(() async {
    _ensureOpen();
    _validatePartition(partitionId);
    if (!now.isUtc || retention <= Duration.zero || limit < 1) {
      throw const OfflineArgumentException();
    }
    final rows = await sql.rawQuery(
      '''SELECT * FROM operations AS op
      WHERE partition_id=? AND terminal_at<=? AND NOT EXISTS
      (SELECT 1 FROM outbox AS ob WHERE ob.partition_id=op.partition_id AND ob.operation_id=op.operation_id)
      ORDER BY terminal_at, operation_id LIMIT ?''',
      [
        partitionId,
        now.microsecondsSinceEpoch - retention.inMicroseconds,
        limit,
      ],
    );
    return rows.map(_operation).toList(growable: false);
  });

  @override
  Future<void> putOperation(OfflineOperationRecord record) => _atomic(() async {
    final id = record.partitionId;
    if (record.generation != await generation(id)) {
      throw const OfflineArgumentException();
    }
    final previous = await getOperation(id, record.operationId);
    if (previous != null && !_sameOperationTopology(previous, record)) {
      throw const OfflineIdentityConflictException(
        OfflineIdentityKind.operation,
      );
    }
    _validateOperationLifecycleCapacity(record, limits);
    for (final item in record.items) {
      final owner = await sql.query(
        'operation_items',
        where: 'partition_id=? AND record_id=?',
        whereArgs: [id, item.recordId],
      );
      if (owner.isNotEmpty &&
          owner.single['operation_id'] != record.operationId) {
        throw const OfflineIdentityConflictException(
          OfflineIdentityKind.record,
        );
      }
      final outbox = await getOutbox(id, item.recordId);
      if (outbox != null &&
          (outbox.operationId != record.operationId ||
              outbox.itemIndex != item.itemIndex)) {
        throw const OfflineIdentityConflictException(
          OfflineIdentityKind.record,
        );
      }
      if (!_terminalWriteState(item.state) && outbox == null) {
        throw const OfflineIdentityConflictException(
          OfflineIdentityKind.operation,
        );
      }
    }
    final retained = await sql.query(
      'outbox',
      columns: ['record_id', 'item_index'],
      where: 'partition_id=? AND operation_id=?',
      whereArgs: [id, record.operationId],
    );
    for (final row in retained) {
      final index = row['item_index']! as int;
      if (index >= record.items.length ||
          record.items[index].recordId != row['record_id']) {
        throw const OfflineIdentityConflictException(
          OfflineIdentityKind.operation,
        );
      }
    }
    await sql.insert(
      'operations',
      _operationColumns(record, limits),
      conflictAlgorithm: ConflictAlgorithm.replace,
    );
    for (final item in record.items) {
      await sql.insert('operation_items', {
        'partition_id': id,
        'record_id': item.recordId,
        'operation_id': record.operationId,
        'item_index': item.itemIndex,
      });
    }
    await _evictOperations(id, record.operationId);
    changed.add(id);
  });

  @override
  Future<void> deleteOperation(String partitionId, String operationId) =>
      _run(() async {
        if (await hasOutboxForOperation(partitionId, operationId)) {
          throw const OfflineArgumentException();
        }
        if (await sql.delete(
              'operations',
              where: 'partition_id=? AND operation_id=?',
              whereArgs: [partitionId, operationId],
            ) !=
            0) {
          changed.add(partitionId);
        }
      });

  @override
  Future<List<OfflineOutboxRecord>> claim(
    String partitionId, {
    required String owner,
    required DateTime now,
    required Duration maxAge,
    required Duration leaseDuration,
    required int limit,
  }) => _atomic(() async {
    if (owner.isEmpty ||
        !_isDurableTime(now) ||
        maxAge <= Duration.zero ||
        leaseDuration <= Duration.zero ||
        limit < 1) {
      throw const OfflineArgumentException();
    }
    _validateLeaseOwnerCapacity(owner, limits);
    if (await replayPausedForAuth(partitionId)) {
      return const <OfflineOutboxRecord>[];
    }
    // Recover only expired leases; aggregate status is updated by the caller
    // in this same transaction, and commit validation enforces the match.
    final expired = await sql.query(
      'outbox',
      where: 'partition_id=? AND state=? AND lease_until<=?',
      whereArgs: [partitionId, 'sending', now.microsecondsSinceEpoch],
    );
    for (final row in expired) {
      await updateOutbox(
        _outbox(row).copyWith(
          state: OfflineOutboxState.enqueued,
          clearLeaseOwner: true,
          clearLeaseUntil: true,
          clearDiagnosticCode: true,
        ),
      );
    }
    final until = _durableDeadline(now, leaseDuration);
    if (until == null) return const <OfflineOutboxRecord>[];
    final rows = await sql.rawQuery(
      '''SELECT * FROM outbox AS candidate
      WHERE candidate.partition_id=? AND candidate.state='enqueued'
      AND (candidate.expiration IS NULL OR candidate.expiration>?)
      AND candidate.enqueued_at<=? AND candidate.enqueued_at>?
      AND (candidate.next_attempt_at IS NULL OR candidate.next_attempt_at<=?)
      AND NOT EXISTS (SELECT 1 FROM outbox AS earlier
        WHERE earlier.partition_id=candidate.partition_id AND earlier.entity_key=candidate.entity_key
        AND earlier.state IN ('enqueued','sending')
        AND (earlier.ordinal<candidate.ordinal
          OR (earlier.ordinal=candidate.ordinal AND earlier.item_index<candidate.item_index)
          OR (earlier.ordinal=candidate.ordinal AND earlier.item_index=candidate.item_index AND earlier.record_id<candidate.record_id)))
      ORDER BY candidate.ordinal,candidate.item_index,candidate.record_id LIMIT ?''',
      [
        partitionId,
        now.microsecondsSinceEpoch,
        now.microsecondsSinceEpoch,
        now.microsecondsSinceEpoch - maxAge.inMicroseconds,
        now.microsecondsSinceEpoch,
        limit,
      ],
    );
    final claimed = <OfflineOutboxRecord>[];
    for (final row in rows) {
      final next = _outbox(row).copyWith(
        state: OfflineOutboxState.sending,
        clearNextAttemptAt: true,
        leaseOwner: owner,
        leaseUntil: until,
        clearDiagnosticCode: true,
      );
      await updateOutbox(next);
      claimed.add(next);
    }
    return List<OfflineOutboxRecord>.unmodifiable(claimed);
  });

  @override
  Future<bool> renewLease(
    String partitionId,
    String recordId, {
    required String owner,
    required int generation,
    required DateTime now,
    required Duration leaseDuration,
  }) => _run(() async {
    if (recordId.isEmpty ||
        owner.isEmpty ||
        !_isDurableTime(now) ||
        leaseDuration <= Duration.zero) {
      throw const OfflineArgumentException();
    }
    _validateLeaseOwnerCapacity(owner, limits);
    final metadata = await _partition(partitionId);
    final record = await getOutbox(partitionId, recordId);
    if (record == null ||
        metadata['generation'] != generation ||
        record.generation != generation ||
        record.state != OfflineOutboxState.sending ||
        record.leaseOwner != owner ||
        record.leaseUntil == null ||
        !now.isBefore(record.leaseUntil!)) {
      return false;
    }
    final until = _durableDeadline(now, leaseDuration);
    if (until != null && until.isAfter(record.leaseUntil!)) {
      await updateOutbox(record.copyWith(leaseUntil: until));
    }
    return true;
  });

  @override
  Future<void> wipePartition(String partitionId) => _atomic(() async {
    final generation = _checkedIncrement(
      (await _partition(partitionId))['generation']! as int,
    );
    for (final table in [
      'cache',
      'recovery',
      'outbox',
      'operations',
      'cdc_origins',
    ]) {
      await sql.delete(
        table,
        where: 'partition_id=?',
        whereArgs: [partitionId],
      );
    }
    await sql.update(
      'partitions',
      {'generation': generation, 'auth_paused': 0, 'change_epoch': 0},
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    enqueued.remove(partitionId);
    changed.add(partitionId);
  });

  Future<bool> _over(
    String table,
    String? partition,
    int count,
    int bytes,
  ) async {
    final row = (await sql.rawQuery(
      'SELECT COUNT(*) AS n, COALESCE(SUM(reserved_bytes),0) AS b FROM $table${partition == null ? '' : ' WHERE partition_id=?'}',
      [?partition],
    )).single;
    return (row['n']! as int) > count || (row['b']! as int) > bytes;
  }

  Future<void> _trimRecovery(String partitionId) async {
    // Reset already removed the corresponding confirmed rows, so dropping an
    // excess marker makes that identity nonresident without exposing stale
    // data. This bounds repeated interrupted checkpoints.
    while (true) {
      final local = await _over(
        'recovery',
        partitionId,
        limits.maxCacheRecordsPerPartition,
        limits.maxCacheBytesPerPartition,
      );
      if (!local &&
          !await _over(
            'recovery',
            null,
            limits.maxCacheRecords,
            limits.maxCacheBytes,
          )) {
        return;
      }
      final rows = await sql.query(
        'recovery',
        columns: ['entity_key'],
        where: 'partition_id=?',
        whereArgs: [partitionId],
        orderBy: 'entity_key',
        limit: 1,
      );
      if (rows.isEmpty) throw const OfflineCapacityException();
      await sql.delete(
        'recovery',
        where: 'partition_id=? AND entity_key=?',
        whereArgs: [partitionId, rows.single['entity_key']],
      );
    }
  }

  Future<void> _evictCache(String id, String key) async {
    while (true) {
      final local = await _over(
        'cache',
        id,
        limits.maxCacheRecordsPerPartition,
        limits.maxCacheBytesPerPartition,
      );
      if (!local &&
          !await _over(
            'cache',
            null,
            limits.maxCacheRecords,
            limits.maxCacheBytes,
          )) {
        return;
      }
      final rows = await sql.rawQuery(
        'SELECT partition_id,entity_key FROM cache WHERE NOT(partition_id=? AND entity_key=?)${local ? ' AND partition_id=?' : ''} ORDER BY accessed_at,partition_id,entity_key LIMIT 1',
        [id, key, if (local) id],
      );
      if (rows.isEmpty) throw const OfflineCapacityException();
      final victim = rows.single;
      await sql.delete(
        'cache',
        where: 'partition_id=? AND entity_key=?',
        whereArgs: [victim['partition_id'], victim['entity_key']],
      );
      changed.add(victim['partition_id']! as String);
    }
  }

  Future<void> _evictOperations(String id, String operation) async {
    while (true) {
      final local = await _over(
        'operations',
        id,
        limits.maxOperationRecordsPerPartition,
        limits.maxOperationBytesPerPartition,
      );
      if (!local &&
          !await _over(
            'operations',
            null,
            limits.maxOperationRecords,
            limits.maxOperationBytes,
          )) {
        return;
      }
      final rows = await sql.rawQuery(
        '''SELECT partition_id,operation_id FROM operations AS op
        WHERE terminal_at IS NOT NULL AND NOT(partition_id=? AND operation_id=?)
        ${local ? ' AND partition_id=?' : ''}
        AND NOT EXISTS (SELECT 1 FROM outbox AS ob WHERE ob.partition_id=op.partition_id AND ob.operation_id=op.operation_id)
        ORDER BY terminal_at,partition_id,operation_id LIMIT 1''',
        [id, operation, if (local) id],
      );
      if (rows.isEmpty) throw const OfflineCapacityException();
      final victim = rows.single;
      await sql.delete(
        'operations',
        where: 'partition_id=? AND operation_id=?',
        whereArgs: [victim['partition_id'], victim['operation_id']],
      );
      changed.add(victim['partition_id']! as String);
    }
  }

  @override
  Future<OfflineChangeCursor> changeCursor(String partitionId) =>
      _run(() async {
        _ensureOpen();
        _validatePartition(partitionId);
        final rows = await sql.query(
          'cdc_origins',
          where: 'partition_id=?',
          whereArgs: [partitionId],
          orderBy: 'origin',
        );
        return OfflineChangeCursor({
          for (final row in rows)
            row['origin']! as String: _progress(row).completedSequence,
        });
      });

  @override
  Future<int> changeEpoch(String partitionId) =>
      _run(() async => (await _partition(partitionId))['change_epoch']! as int);

  @override
  Future<bool> hasUnknownResident(String partitionId, OfflineEntityKey key) =>
      _run(() async {
        _ensureOpen();
        _validatePartition(partitionId);
        final rows = await sql.query(
          'recovery',
          columns: ['entity_key'],
          where: 'partition_id=? AND entity_key=?',
          whereArgs: [partitionId, key.canonical],
          limit: 1,
        );
        return rows.isNotEmpty;
      });

  @override
  Future<List<OfflineEntityKey>> unknownResidents(
    String partitionId, {
    required int limit,
  }) => _run(() async {
    _ensureOpen();
    _validatePartition(partitionId);
    if (limit < 1 || limit > offlineMaxResidentRevalidationBatch) {
      throw const OfflineArgumentException();
    }
    final rows = await sql.query(
      'recovery',
      columns: ['entity_key'],
      where: 'partition_id=?',
      whereArgs: [partitionId],
      orderBy: 'entity_key',
      limit: limit,
    );
    return List<OfflineEntityKey>.unmodifiable(
      rows.map(
        (row) => OfflineEntityKey.fromCanonical(row['entity_key']! as String),
      ),
    );
  });

  @override
  Future<bool> completeUnknownResident(
    String partitionId,
    OfflineEntityKey key, {
    required int expectedEpoch,
  }) => _atomic(() async {
    final metadata = await _partition(partitionId);
    if (metadata['change_epoch'] != expectedEpoch) return false;
    final deleted = await sql.delete(
      'recovery',
      where: 'partition_id=? AND entity_key=?',
      whereArgs: [partitionId, key.canonical],
    );
    if (deleted == 0) return false;
    changed.add(partitionId);
    return true;
  });

  @override
  Future<void> applyChangeChunk(
    String partitionId,
    OfflineChangeChunk chunk,
  ) => _atomic(() async {
    await _partition(partitionId);
    final rows = await sql.query(
      'cdc_origins',
      where: 'partition_id=? AND origin=?',
      whereArgs: [partitionId, chunk.origin],
    );
    final previous = rows.isEmpty
        ? OfflineChangeProgress(completedSequence: BigInt.zero)
        : _progress(rows.single);
    final next = previous.accept(chunk);
    if (next == null) return;
    if (rows.isEmpty) {
      final count =
          (await sql.rawQuery(
                'SELECT COUNT(*) AS n FROM cdc_origins WHERE partition_id=?',
                [partitionId],
              )).single['n']!
              as int;
      final globalCount =
          (await sql.rawQuery(
                'SELECT COUNT(*) AS n FROM cdc_origins',
              )).single['n']!
              as int;
      if (count >= offlineMaxChangeOrigins ||
          globalCount >= offlineMaxChangeOriginsPerStore) {
        throw const OfflineCapacityException();
      }
    }
    for (final key in chunk.keys) {
      await sql.delete(
        'cache',
        where: 'partition_id=? AND entity_key=?',
        whereArgs: [partitionId, key.canonical],
      );
    }
    for (final prefix in chunk.vertexPrefixes) {
      // Binary UTF-16 ranges preserve literal Dart prefix semantics, including
      // NUL, SQL wildcards, and a prefix ending within a surrogate pair.
      final bytes = _prefixBytes(prefix);
      final end = _prefixEnd(bytes);
      await sql.delete(
        'cache',
        where:
            'partition_id=? AND vertex_key>=?${end == null ? '' : ' AND vertex_key<?'}',
        whereArgs: [partitionId, bytes, ?end],
      );
    }
    await sql.insert(
      'cdc_origins',
      _progressColumns(partitionId, chunk.origin, next),
      conflictAlgorithm: ConflictAlgorithm.replace,
    );
    final epoch = _checkedIncrement(
      (await _partition(partitionId))['change_epoch']! as int,
    );
    await sql.update(
      'partitions',
      {'change_epoch': epoch},
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    changed.add(partitionId);
  });

  @override
  Future<void> resetChangeCursor(
    String partitionId,
    OfflineChangeCursor checkpoint,
  ) => _atomic(() async {
    await _partition(partitionId);
    final counts = (await sql.rawQuery(
      'SELECT COUNT(*) AS total, COALESCE(SUM(CASE WHEN partition_id=? THEN 1 ELSE 0 END),0) AS own FROM cdc_origins',
      [partitionId],
    )).single;
    if ((counts['total']! as int) -
            (counts['own']! as int) +
            checkpoint.sequences.length >
        offlineMaxChangeOriginsPerStore) {
      throw const OfflineCapacityException();
    }
    await sql.rawInsert(
      'INSERT OR IGNORE INTO recovery(partition_id,entity_key,reserved_bytes) '
      'SELECT partition_id,entity_key,length(CAST(entity_key AS BLOB))+32 '
      'FROM cache WHERE partition_id=?',
      [partitionId],
    );
    await sql.delete(
      'cache',
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    await _trimRecovery(partitionId);
    await sql.delete(
      'cdc_origins',
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    for (final entry in checkpoint.sequences.entries) {
      await sql.insert(
        'cdc_origins',
        _progressColumns(
          partitionId,
          entry.key,
          OfflineChangeProgress(completedSequence: entry.value),
        ),
      );
    }
    final epoch = _checkedIncrement(
      (await _partition(partitionId))['change_epoch']! as int,
    );
    await sql.update(
      'partitions',
      {'change_epoch': epoch},
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    changed.add(partitionId);
  });

  Future<void> validateAll() async {
    String? cursor;
    while (true) {
      final rows = await sql.query(
        'partitions',
        where: cursor == null ? null : 'partition_id>?',
        whereArgs: cursor == null ? null : [cursor],
        orderBy: 'partition_id',
        limit: 128,
      );
      if (rows.isEmpty) break;
      for (final row in rows) {
        await _validatePartitionGraph(row['partition_id']! as String);
      }
      cursor = rows.last['partition_id']! as String;
    }
    await _validateCapacities();
  }

  Future<void> validateCommit() async {
    try {
      for (final id in {...changed, ...mutated}) {
        await _validatePartitionGraph(id);
      }
      for (final partition in enqueued.entries) {
        for (final entry in partition.value.entries) {
          final rows = await sql.query(
            'operations',
            where: 'partition_id=? AND operation_id=?',
            whereArgs: [partition.key, entry.key],
          );
          if (rows.isEmpty) throw const OfflineDurableGraphException();
          final operation = _operation(rows.single);
          final records = entry.value;
          if (operation.items.length != records.length) {
            throw const OfflineDurableGraphException();
          }
          for (var i = 0; i < records.length; i++) {
            final record = records[i];
            final item = operation.items[i];
            if (item.recordId != record.recordId ||
                item.operationId != record.operationId ||
                item.itemIndex != i ||
                operation.updatedAt.isBefore(record.enqueuedAt) ||
                (operation.terminalAt?.isBefore(record.enqueuedAt) ?? false) ||
                (record.state == OfflineOutboxState.expired &&
                    (item.state != OfflineWriteState.expired ||
                        item.attemptCount != record.attemptCount ||
                        item.diagnosticCode != record.diagnosticCode))) {
              throw const OfflineDurableGraphException();
            }
          }
        }
      }
      await _validateCapacities();
    } on OfflineCapacityException {
      rethrow;
    } on DatabaseException catch (error, stack) {
      Error.throwWithStackTrace(_safeDatabaseError(error), stack);
    } on SqliteOfflineStoreException {
      rethrow;
    } on Object {
      throw const OfflineDurableGraphException();
    }
  }

  Future<void> _validateCapacities() async {
    final origins =
        (await sql.rawQuery(
              'SELECT COUNT(*) AS n FROM cdc_origins',
            )).single['n']!
            as int;
    if (origins > offlineMaxChangeOriginsPerStore) {
      throw const OfflineCapacityException();
    }
    for (final item in <(String, int, int, int, int)>[
      (
        'cache',
        limits.maxCacheRecords,
        limits.maxCacheBytes,
        limits.maxCacheRecordsPerPartition,
        limits.maxCacheBytesPerPartition,
      ),
      (
        'recovery',
        limits.maxCacheRecords,
        limits.maxCacheBytes,
        limits.maxCacheRecordsPerPartition,
        limits.maxCacheBytesPerPartition,
      ),
      (
        'outbox',
        limits.maxOutboxRecords,
        limits.maxOutboxBytes,
        limits.maxOutboxRecordsPerPartition,
        limits.maxOutboxBytesPerPartition,
      ),
      (
        'operations',
        limits.maxOperationRecords,
        limits.maxOperationBytes,
        limits.maxOperationRecordsPerPartition,
        limits.maxOperationBytesPerPartition,
      ),
    ]) {
      if (await _over(item.$1, null, item.$2, item.$3) ||
          (await sql.rawQuery(
            'SELECT 1 FROM ${item.$1} GROUP BY partition_id HAVING COUNT(*)>? OR SUM(reserved_bytes)>? LIMIT 1',
            [item.$4, item.$5],
          )).isNotEmpty) {
        throw const OfflineCapacityException();
      }
    }
  }

  Future<void> _validatePartitionGraph(String id) async {
    final metadata = (await sql.query(
      'partitions',
      where: 'partition_id=?',
      whereArgs: [id],
    )).single;
    final generation = metadata['generation']! as int;
    final ordinal = metadata['next_ordinal']! as int;
    final version = metadata['version']! as int;
    if (id.isEmpty ||
        generation < 0 ||
        ordinal < 0 ||
        version < 0 ||
        metadata['change_epoch'] is! int ||
        (metadata['change_epoch']! as int) < 0 ||
        (metadata['auth_paused'] != 0 && metadata['auth_paused'] != 1)) {
      throw const OfflineCodecException();
    }
    await _eachRow('cache', 'entity_key', id, (row) async {
      final record = _cache(row);
      _validateCacheIdentity(record);
      if (record.partitionId != id ||
          record.generation != generation ||
          !_columnsMatch(
            row,
            _cacheColumns(record, _cacheAdmissionBytesFor(record)),
          )) {
        throw const OfflineCodecException();
      }
    });
    await _eachRow('recovery', 'entity_key', id, (row) async {
      final encoded = row['entity_key'];
      if (encoded is! String ||
          row['reserved_bytes'] != utf8.encode(encoded).length + 32) {
        throw const OfflineCodecException();
      }
      OfflineEntityKey.fromCanonical(encoded);
    });
    await _eachRow('operations', 'operation_id', id, (row) async {
      final operation = _operation(row);
      if (operation.partitionId != id ||
          operation.generation != generation ||
          !_columnsMatch(row, _operationColumns(operation, limits))) {
        throw const OfflineCodecException();
      }
      final indexedItems = await sql.query(
        'operation_items',
        where: 'partition_id=? AND operation_id=?',
        whereArgs: [id, operation.operationId],
        orderBy: 'item_index',
      );
      if (indexedItems.length != operation.items.length) {
        throw const OfflineCodecException();
      }
      for (var i = 0; i < operation.items.length; i++) {
        final item = operation.items[i];
        if (indexedItems[i]['record_id'] != item.recordId ||
            indexedItems[i]['item_index'] != i) {
          throw const OfflineCodecException();
        }
        final records = await sql.query(
          'outbox',
          where: 'partition_id=? AND record_id=?',
          whereArgs: [id, item.recordId],
        );
        if (records.isEmpty && !_terminalWriteState(item.state)) {
          throw const OfflineCodecException();
        }
      }
    });
    await _eachRow('outbox', 'record_id', id, (row) async {
      final record = _outbox(row);
      if (record.partitionId != id ||
          record.generation != generation ||
          record.ordinal < 1 ||
          record.ordinal > ordinal ||
          record.state == OfflineOutboxState.expired ||
          !_columnsMatch(row, _outboxColumns(record, limits)) ||
          (record.intent is OfflineAddEdgeIntent &&
              !_isQuarantinedLegacyAdd(record))) {
        throw const OfflineCodecException();
      }
      final rows = await sql.query(
        'operations',
        where: 'partition_id=? AND operation_id=?',
        whereArgs: [id, record.operationId],
      );
      if (rows.isEmpty) throw const OfflineCodecException();
      final operation = _operation(rows.single);
      if (record.itemIndex >= operation.items.length ||
          !_outboxStatusMatches(record, operation.items[record.itemIndex]) ||
          operation.updatedAt.isBefore(record.enqueuedAt) ||
          (record.deadLetteredAt?.isAfter(operation.updatedAt) ?? false) ||
          (operation.terminalAt?.isBefore(record.enqueuedAt) ?? false) ||
          (record.deadLetteredAt != null &&
              operation.terminalAt != null &&
              record.deadLetteredAt!.isAfter(operation.terminalAt!))) {
        throw const OfflineCodecException();
      }
      if (record.state == OfflineOutboxState.enqueued &&
          metadata['auth_paused'] == 0 &&
          (record.diagnosticCode == 'unauthenticated' ||
              operation.items[record.itemIndex].state ==
                  OfflineWriteState.pausedForAuth)) {
        throw const OfflineCodecException();
      }
    });
    if ((await sql.rawQuery(
          'SELECT 1 FROM outbox WHERE partition_id=? GROUP BY ordinal HAVING COUNT(DISTINCT operation_id)>1 LIMIT 1',
          [id],
        )).isNotEmpty ||
        (await sql.rawQuery(
          'SELECT 1 FROM outbox WHERE partition_id=? GROUP BY operation_id HAVING COUNT(DISTINCT ordinal)>1 LIMIT 1',
          [id],
        )).isNotEmpty) {
      throw const OfflineCodecException();
    }
    final progress = await sql.query(
      'cdc_origins',
      where: 'partition_id=?',
      whereArgs: [id],
    );
    if (progress.length > offlineMaxChangeOrigins) {
      throw const OfflineCapacityException();
    }
    OfflineChangeCursor({
      for (final row in progress)
        row['origin']! as String: _progress(row).completedSequence,
    });
  }

  Future<void> _eachRow(
    String table,
    String key,
    String id,
    Future<void> Function(Map<String, Object?>) action,
  ) async {
    String? cursor;
    while (true) {
      final rows = await sql.query(
        table,
        where: 'partition_id=?${cursor == null ? '' : ' AND $key>?'}',
        whereArgs: [id, ?cursor],
        orderBy: key,
        limit: 128,
      );
      if (rows.isEmpty) return;
      for (final row in rows) {
        await action(row);
      }
      cursor = rows.last[key]! as String;
    }
  }
}

Map<String, Object?> _cacheColumns(OfflineCacheRecord record, int bytes) => {
  'partition_id': record.partitionId,
  'entity_key': record.key.canonical,
  'generation': record.generation,
  'accessed_at': record.lastAccessAt.microsecondsSinceEpoch,
  'vertex_key': record.key.vertexKey == null
      ? null
      : _prefixBytes(record.key.vertexKey!),
  'reserved_bytes': bytes,
  'payload': _encode(OfflineCodec.encodeCacheRecord(record)),
};
Map<String, Object?> _outboxColumns(
  OfflineOutboxRecord record,
  OfflineStoreLimits limits,
) => {
  'partition_id': record.partitionId,
  'record_id': record.recordId,
  'operation_id': record.operationId,
  'item_index': record.itemIndex,
  'entity_key': record.intent.key.canonical,
  'generation': record.generation,
  'ordinal': record.ordinal,
  'state': record.state.name,
  'enqueued_at': record.enqueuedAt.microsecondsSinceEpoch,
  'expiration': record.absoluteExpiration?.microsecondsSinceEpoch,
  'next_attempt_at': record.nextAttemptAt?.microsecondsSinceEpoch,
  'lease_until': record.leaseUntil?.microsecondsSinceEpoch,
  'dead_lettered_at': record.deadLetteredAt?.microsecondsSinceEpoch,
  'reserved_bytes': _outboxAdmissionBytesFor(record, limits),
  'payload': _encode(OfflineCodec.encodeOutboxRecord(record)),
};
Map<String, Object?> _operationColumns(
  OfflineOperationRecord record,
  OfflineStoreLimits limits,
) => {
  'partition_id': record.partitionId,
  'operation_id': record.operationId,
  'generation': record.generation,
  'updated_at': record.updatedAt.microsecondsSinceEpoch,
  'terminal_at': record.terminalAt?.microsecondsSinceEpoch,
  'reserved_bytes': _operationAdmissionBytesFor(record, limits),
  'payload': _encode(OfflineCodec.encodeOperationRecord(record)),
};
OfflineChangeProgress _progress(Map<String, Object?> row) =>
    OfflineChangeProgress.fromJson({
      'completed': row['completed'],
      'pending': row['pending'],
      'nextChunk': row['next_chunk'],
    });
Map<String, Object?> _progressColumns(
  String partition,
  String origin,
  OfflineChangeProgress progress,
) => {
  'partition_id': partition,
  'origin': origin,
  'completed': progress.completedSequence.toString(),
  'pending': progress.pendingSequence?.toString(),
  'next_chunk': progress.nextChunk,
};
bool _columnsMatch(Map<String, Object?> row, Map<String, Object?> expected) =>
    row.length == expected.length &&
    expected.entries.every((entry) {
      final actual = row[entry.key];
      final value = entry.value;
      if (actual is List<int> && value is List<int>) {
        if (actual.length != value.length) return false;
        for (var i = 0; i < actual.length; i++) {
          if (actual[i] != value[i]) return false;
        }
        return true;
      }
      return actual == value;
    });
