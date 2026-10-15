part of '../lantern_client_offline_sqlite.dart';

final class _SqlTransaction
    implements OfflineStoreTransaction, OfflineStoreClaimTransaction {
  _SqlTransaction(this.sql, this.limits, [this.claimLane, this.storeOpen]);
  final _SqlLane? claimLane;
  final bool Function()? storeOpen;
  final List<_SqlClaimGuard> pendingFences = [];
  final Set<String> observationInvalidated = {};
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
  Future<DateTime> effectiveTime(String partitionId, DateTime observedAt) =>
      _run(() async {
        if (!_isDurableTime(observedAt)) throw const OfflineArgumentException();
        final metadata = await _partition(partitionId);
        final prior = metadata['time_high_water']! as int;
        final next = observedAt.microsecondsSinceEpoch > prior
            ? observedAt.microsecondsSinceEpoch
            : prior;
        if (next != prior) {
          await sql.update(
            'partitions',
            {'time_high_water': next},
            where: 'partition_id=?',
            whereArgs: [partitionId],
          );
          mutated.add(partitionId);
        }
        return DateTime.fromMicrosecondsSinceEpoch(next, isUtc: true);
      });

  @override
  Future<String?> authorityBinding(String partitionId) => _run(
    () async => (await _partition(partitionId))['authority_binding'] as String?,
  );

  @override
  Future<OfflineClaimFence> captureClaimFence(
    OfflineOutboxRecord claim, {
    required String authorityBinding,
  }) => _run(() async {
    final lane = claimLane;
    if (lane == null || storeOpen == null) {
      throw const OfflineUnsupportedOperationException();
    }
    if (lane.fences.length + pendingFences.length >= limits.maxOutboxRecords) {
      throw const OfflineCapacityException();
    }
    final guard = _SqlClaimGuard(claim, authorityBinding, storeOpen!, lane);
    pendingFences.add(guard);
    return guard.fence;
  });

  @override
  Future<void> bindAuthority(String partitionId, String authorityBinding) =>
      _run(() async {
        if (authorityBinding.isEmpty ||
            utf8.encode(authorityBinding).length > 4096) {
          throw const OfflineArgumentException();
        }
        final p = await _partition(partitionId);
        if (p['authority_binding'] == authorityBinding) return;
        var unqualified = p['authority_binding'] != null;
        for (final table in [
          'cache',
          'traversals',
          'recovery',
          'cdc_scoped',
          'cdc_origins',
        ]) {
          if (unqualified) break;
          unqualified = (await sql.query(
            table,
            columns: ['partition_id'],
            where: 'partition_id=?',
            whereArgs: [partitionId],
            limit: 1,
          )).isNotEmpty;
        }
        if (unqualified) await resetScopedChangeCursor(partitionId, null);
        await sql.update(
          'partitions',
          {'authority_binding': authorityBinding, 'policy_paused': 0},
          where: 'partition_id=?',
          whereArgs: [partitionId],
        );
        changed.add(partitionId);
        observationInvalidated.add(partitionId);
      });

  @override
  Future<void> invalidateMutationCache(
    String partitionId,
    OfflineEntityKey key,
  ) => _run(() async {
    await deleteCache(partitionId, key);
    if (key.kind == OfflineEntityKind.vertex) {
      await sql.delete(
        'cache',
        where: 'partition_id=? AND entity_key>=? AND entity_key<?',
        whereArgs: [partitionId, 'e:', 'e;'],
      );
    }
    final p = await _partition(partitionId);
    await sql.update(
      'partitions',
      {'change_epoch': _checkedIncrement(p['change_epoch']! as int)},
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    await invalidateTraversals(partitionId);
    changed.add(partitionId);
    observationInvalidated.add(partitionId);
  });

  @override
  Future<bool> replayPausedForAuth(String partitionId) =>
      _run(() async => (await _partition(partitionId))['auth_paused'] == 1);

  @override
  Future<void> setReplayPausedForAuth(
    String partitionId,
    bool paused, {
    int? credentialGeneration,
  }) => _run(() async {
    final metadata = await _partition(partitionId);
    if (credentialGeneration != null && credentialGeneration < 0) {
      throw const OfflineArgumentException();
    }
    final next = paused
        ? credentialGeneration ?? metadata['paused_credential_generation']
        : null;
    if (metadata['auth_paused'] == (paused ? 1 : 0) &&
        metadata['paused_credential_generation'] == next) {
      return;
    }
    await sql.update(
      'partitions',
      {'auth_paused': paused ? 1 : 0, 'paused_credential_generation': next},
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    changed.add(partitionId);
    observationInvalidated.add(partitionId);
  });

  @override
  Future<OfflineAutomaticSchedule> automaticSchedule(
    String partitionId, {
    required DateTime now,
    required Duration maxAge,
  }) => _run(() async {
    await _partition(partitionId);
    final row = (await sql.rawQuery(
      """
      SELECT MIN(CASE WHEN state='sending' THEN lease_until
          ELSE COALESCE(next_attempt_at,?) END) AS attempt,
        MIN(CASE WHEN expiration>? THEN expiration END) AS expiration,
        MIN(CASE WHEN enqueued_at+?>? THEN enqueued_at+? END) AS age
      FROM outbox WHERE partition_id=? AND state IN ('enqueued','sending')
      """,
      [
        now.millisecondsSinceEpoch,
        now.millisecondsSinceEpoch,
        maxAge.inMilliseconds,
        now.millisecondsSinceEpoch,
        maxAge.inMilliseconds,
        partitionId,
      ],
    )).single;
    DateTime? time(String key) => row[key] == null
        ? null
        : DateTime.fromMillisecondsSinceEpoch(row[key]! as int, isUtc: true);
    final expiration = time('expiration'), age = time('age');
    return OfflineAutomaticSchedule(
      nextAttemptAt: time('attempt'),
      nextLocalExpiry: expiration == null
          ? age
          : age == null || expiration.isBefore(age)
          ? expiration
          : age,
    );
  });

  @override
  Future<int?> pausedCredentialGeneration(String partitionId) => _run(
    () async =>
        (await _partition(partitionId))['paused_credential_generation'] as int?,
  );
  @override
  Future<bool> replayPausedForPolicy(String partitionId) =>
      _run(() async => (await _partition(partitionId))['policy_paused'] == 1);
  @override
  Future<void> setReplayPausedForPolicy(String partitionId, bool paused) =>
      _run(() async {
        final metadata = await _partition(partitionId);
        if (metadata['policy_paused'] == (paused ? 1 : 0)) return;
        await sql.update(
          'partitions',
          {'policy_paused': paused ? 1 : 0},
          where: 'partition_id=?',
          whereArgs: [partitionId],
        );
        changed.add(partitionId);
        observationInvalidated.add(partitionId);
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
            preparation: record.preparation,
            mayHaveDispatched: record.mayHaveDispatched,
            dependencies: record.dependencies,
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
        for (final d in record.dependencies) {
          await sql.insert('operation_dependencies', {
            'partition_id': record.partitionId,
            'record_id': record.recordId,
            'prerequisite_operation_id': d.operationId,
            'prerequisite_item': d.itemIndex,
            'predicate': d.predicate.name,
          });
        }
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
    observationInvalidated.add(id);
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
    observationInvalidated.add(record.partitionId);
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
        observationInvalidated.add(partitionId);
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
      WHERE partition_id=? AND terminal_at<=?
      AND (guard_until IS NULL OR guard_until<=?)
      AND NOT EXISTS (SELECT 1 FROM operation_dependencies d WHERE d.partition_id=op.partition_id AND d.prerequisite_operation_id=op.operation_id)
      AND NOT EXISTS
      (SELECT 1 FROM outbox AS ob WHERE ob.partition_id=op.partition_id AND ob.operation_id=op.operation_id)
      ORDER BY terminal_at, operation_id LIMIT ?''',
      [
        partitionId,
        now.microsecondsSinceEpoch - retention.inMicroseconds,
        now.microsecondsSinceEpoch,
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
    if (previous != null &&
        (!_sameOperationTopology(previous, record) ||
            !OfflineCodec.monotoneOperationProgress(previous, record))) {
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
  Future<void> deleteOperation(String partitionId, String operationId) => _run(
    () async {
      final op = await getOperation(partitionId, operationId);
      final clock = (await _partition(partitionId))['time_high_water']! as int;
      if (await hasOutboxForOperation(partitionId, operationId) ||
          (op?.guardUntil?.microsecondsSinceEpoch ?? 0) > clock ||
          (await sql.query(
            'operation_dependencies',
            columns: ['record_id'],
            where: 'partition_id=? AND prerequisite_operation_id=?',
            whereArgs: [partitionId, operationId],
            limit: 1,
          )).isNotEmpty) {
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
    },
  );

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
    if (await replayPausedForAuth(partitionId) ||
        await replayPausedForPolicy(partitionId)) {
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
      'traversals',
      'recovery',
      'outbox',
      'operations',
      'cdc_origins',
      'cdc_scoped',
    ]) {
      await sql.delete(
        table,
        where: 'partition_id=?',
        whereArgs: [partitionId],
      );
    }
    await sql.update(
      'partitions',
      {
        'generation': generation,
        'auth_paused': 0,
        'change_epoch': 0,
        'traversal_epoch': 0,
      },
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    enqueued.remove(partitionId);
    changed.add(partitionId);
    observationInvalidated.add(partitionId);
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
        AND (guard_until IS NULL OR guard_until <= (SELECT time_high_water FROM partitions p WHERE p.partition_id=op.partition_id))
        AND NOT EXISTS (SELECT 1 FROM operation_dependencies d WHERE d.partition_id=op.partition_id AND d.prerequisite_operation_id=op.operation_id)
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
  Future<OfflineScopedChangeCursor?> scopedChangeCursor(String partitionId) =>
      _run(() async {
        _ensureOpen();
        _validatePartition(partitionId);
        final rows = await sql.query(
          'cdc_scoped',
          columns: ['cursor'],
          where: 'partition_id=?',
          whereArgs: [partitionId],
          limit: 1,
        );
        if (rows.isEmpty) return null;
        final bytes = rows.single['cursor'];
        if (bytes is! List<int>) throw const OfflineCodecException();
        return OfflineScopedChangeCursor(bytes);
      });

  @override
  Future<void> applyScopedChangeFrame(
    String partitionId,
    OfflineScopedChangeFrame frame,
  ) => _atomic(() async {
    final metadata = await _partition(partitionId);
    if (frame.bootstrap || await scopedChangeCursor(partitionId) == null) {
      throw const OfflineChangeGapException();
    }
    for (final key in frame.keys) {
      await sql.delete(
        'cache',
        where: 'partition_id=? AND entity_key=?',
        whereArgs: [partitionId, key.canonical],
      );
      if (key.kind == OfflineEntityKind.vertex) {
        await sql.delete(
          'cache',
          where: 'partition_id=? AND entity_key>=? AND entity_key<?',
          whereArgs: [partitionId, 'e:', 'e;'],
        );
      }
    }
    if (frame.cursor != null) {
      await sql.update(
        'cdc_scoped',
        {'cursor': frame.cursor!.toBytes()},
        where: 'partition_id=?',
        whereArgs: [partitionId],
      );
    }
    if (frame.keys.isNotEmpty) {
      await sql.update(
        'partitions',
        {'change_epoch': _checkedIncrement(metadata['change_epoch']! as int)},
        where: 'partition_id=?',
        whereArgs: [partitionId],
      );
      await invalidateTraversals(partitionId);
      observationInvalidated.add(partitionId);
    }
    changed.add(partitionId);
  });

  @override
  Future<void> resetScopedChangeCursor(
    String partitionId,
    OfflineScopedChangeCursor? checkpoint,
  ) => _atomic(() async {
    await _partition(partitionId);
    if (checkpoint != null && await scopedChangeCursor(partitionId) == null) {
      final count =
          (await sql.rawQuery(
                'SELECT COUNT(*) AS n FROM cdc_scoped',
              )).single['n']!
              as int;
      if (count >= offlineMaxScopedCursorsPerStore) {
        throw const OfflineCapacityException();
      }
    }
    await resetChangeCursor(partitionId, OfflineChangeCursor(const {}));
    await sql.delete(
      'cdc_scoped',
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    if (checkpoint != null) {
      await sql.insert('cdc_scoped', {
        'partition_id': partitionId,
        'cursor': checkpoint.toBytes(),
      });
    }
  });

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
  Future<int> traversalValidityEpoch(String partitionId) => _run(
    () async => (await _partition(partitionId))['traversal_epoch']! as int,
  );

  @override
  Future<void> invalidateTraversals(String partitionId) => _atomic(() async {
    final p = await _partition(partitionId);
    await sql.update(
      'partitions',
      {'traversal_epoch': _checkedIncrement(p['traversal_epoch']! as int)},
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    changed.add(partitionId);
    observationInvalidated.add(partitionId);
  });

  @override
  Future<OfflineTraversalRecord?> getTraversal(
    String partitionId,
    String queryKey,
  ) => _run(() async {
    _ensureOpen();
    _validatePartition(partitionId);
    final rows = await sql.query(
      'traversals',
      where: 'partition_id=? AND query_key=?',
      whereArgs: [partitionId, queryKey],
    );
    return rows.isEmpty ? null : _traversal(rows.single);
  });

  @override
  Future<void> putTraversal(
    String partitionId,
    OfflineTraversalRecord record,
  ) => _atomic(() async {
    final p = await _partition(partitionId);
    if (record.partitionId != partitionId ||
        record.generation != p['generation'] ||
        record.validityEpoch != p['traversal_epoch'] ||
        record.authorityBinding != p['authority_binding']) {
      throw const OfflineArgumentException();
    }
    final bytes = OfflineTraversalCodec.reservedBytes(record);
    if (bytes > limits.maxTraversalBytes ||
        bytes > limits.maxTraversalBytesPerPartition ||
        limits.maxTraversalRecords == 0 ||
        limits.maxTraversalRecordsPerPartition == 0) {
      throw const OfflineCapacityException();
    }
    await sql.insert(
      'traversals',
      _traversalColumns(record),
      conflictAlgorithm: ConflictAlgorithm.replace,
    );
    while (true) {
      final local = await _over(
        'traversals',
        partitionId,
        limits.maxTraversalRecordsPerPartition,
        limits.maxTraversalBytesPerPartition,
      );
      if (!local &&
          !await _over(
            'traversals',
            null,
            limits.maxTraversalRecords,
            limits.maxTraversalBytes,
          )) {
        break;
      }
      final victims = await sql.rawQuery(
        'SELECT partition_id,query_key FROM traversals WHERE NOT(partition_id=? AND query_key=?)'
        "${local ? ' AND partition_id=?' : ''} ORDER BY accessed_at,partition_id,query_key LIMIT 1",
        [partitionId, record.queryKey, if (local) partitionId],
      );
      if (victims.isEmpty) throw const OfflineCapacityException();
      final victim = victims.single;
      await sql.delete(
        'traversals',
        where: 'partition_id=? AND query_key=?',
        whereArgs: [victim['partition_id'], victim['query_key']],
      );
      changed.add(victim['partition_id']! as String);
    }
    changed.add(partitionId);
  });

  @override
  Future<void> touchTraversal(
    String partitionId,
    String queryKey,
    DateTime accessedAt,
  ) => _run(() async {
    final record = await getTraversal(partitionId, queryKey);
    if (record == null) return;
    await sql.update(
      'traversals',
      _traversalColumns(record.accessedAt(accessedAt)),
      where: 'partition_id=? AND query_key=?',
      whereArgs: [partitionId, queryKey],
    );
    mutated.add(partitionId);
  });

  @override
  Future<int> changeEpoch(String partitionId) =>
      _run(() async => (await _partition(partitionId))['change_epoch']! as int);

  @override
  Future<void> advanceReadEpoch(String partitionId) => _atomic(() async {
    final p = await _partition(partitionId);
    await sql.update(
      'partitions',
      {'change_epoch': _checkedIncrement(p['change_epoch']! as int)},
      where: 'partition_id=?',
      whereArgs: [partitionId],
    );
    changed.add(partitionId);
    observationInvalidated.add(partitionId);
  });

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
    String? after,
  }) => _run(() async {
    _ensureOpen();
    _validatePartition(partitionId);
    if (limit < 1 || limit > offlineMaxResidentRevalidationBatch) {
      throw const OfflineArgumentException();
    }
    final rows = await sql.query(
      'recovery',
      columns: ['entity_key'],
      where: after == null
          ? 'partition_id=?'
          : 'partition_id=? AND entity_key>?',
      whereArgs: [partitionId, ?after],
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
    if (await scopedChangeCursor(partitionId) != null) {
      throw const OfflineChangeGapException();
    }
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
    if (chunk.keys.isNotEmpty || chunk.vertexPrefixes.isNotEmpty) {
      await invalidateTraversals(partitionId);
    }
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
    observationInvalidated.add(partitionId);
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
    await invalidateTraversals(partitionId);
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
    await sql.delete(
      'cdc_scoped',
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
    observationInvalidated.add(partitionId);
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
    final scoped =
        (await sql.rawQuery(
              'SELECT COUNT(*) AS n FROM cdc_scoped',
            )).single['n']!
            as int;
    if (scoped > offlineMaxScopedCursorsPerStore) {
      throw const OfflineCapacityException();
    }

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
        'traversals',
        limits.maxTraversalRecords,
        limits.maxTraversalBytes,
        limits.maxTraversalRecordsPerPartition,
        limits.maxTraversalBytesPerPartition,
      ),
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
    validateOfflineDependencies(
      (await sql.query(
        'outbox',
        where: 'partition_id=?',
        whereArgs: [id],
      )).map(_outbox).toList(),
      (await sql.query(
        'operations',
        where: 'partition_id=?',
        whereArgs: [id],
      )).map(_operation).toList(),
    );
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
        metadata['time_high_water'] is! int ||
        (metadata['time_high_water']! as int) < 0 ||
        metadata['authority_binding'] != null &&
            (metadata['authority_binding'] is! String ||
                (metadata['authority_binding']! as String).isEmpty ||
                utf8.encode(metadata['authority_binding']! as String).length >
                    4096) ||
        metadata['traversal_epoch'] is! int ||
        (metadata['traversal_epoch']! as int) < 0 ||
        metadata['change_epoch'] is! int ||
        (metadata['change_epoch']! as int) < 0 ||
        (metadata['policy_paused'] != 0 && metadata['policy_paused'] != 1) ||
        (metadata['paused_credential_generation'] != null &&
            (metadata['paused_credential_generation'] is! int ||
                (metadata['paused_credential_generation']! as int) < 0 ||
                metadata['auth_paused'] != 1)) ||
        (metadata['auth_paused'] != 0 && metadata['auth_paused'] != 1)) {
      throw const OfflineCodecException();
    }
    await _eachRow('traversals', 'query_key', id, (row) async {
      final record = _traversal(row);
      if (record.partitionId != id ||
          record.generation != generation ||
          record.validityEpoch > (metadata['traversal_epoch']! as int) ||
          !_columnsMatch(row, _traversalColumns(record))) {
        throw const OfflineCodecException();
      }
    });
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
      final deps = await sql.query(
        'operation_dependencies',
        where: 'partition_id=? AND record_id=?',
        whereArgs: [id, record.recordId],
      );
      final expected = {
        for (final d in record.dependencies)
          '${d.operationId}:${d.itemIndex}:${d.predicate.name}',
      };
      if (deps.length != expected.length ||
          deps.any(
            (d) => !expected.contains(
              '${d['prerequisite_operation_id']}:${d['prerequisite_item']}:${d['predicate']}',
            ),
          )) {
        throw const OfflineCodecException();
      }

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
      if (operation.kind != null &&
          record.mayHaveDispatched !=
              operation.items[record.itemIndex].mayHaveDispatched) {
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
    final scopedRows = await sql.query(
      'cdc_scoped',
      columns: ['cursor'],
      where: 'partition_id=?',
      whereArgs: [id],
      limit: 1,
    );
    if (scopedRows.isNotEmpty) {
      final bytes = scopedRows.single['cursor'];
      if (bytes is! List<int>) throw const OfflineCodecException();
      OfflineScopedChangeCursor(bytes);
    }
    final progress = await sql.query(
      'cdc_origins',
      where: 'partition_id=?',
      whereArgs: [id],
    );
    if (scopedRows.isNotEmpty && progress.isNotEmpty) {
      throw const OfflineCodecException();
    }
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
  'guard_until': record.guardUntil?.microsecondsSinceEpoch,
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

OfflineTraversalRecord _traversal(Map<String, Object?> row) =>
    OfflineTraversalCodec.decode(_decode(row['payload']));
Map<String, Object?> _traversalColumns(OfflineTraversalRecord r) => {
  'partition_id': r.partitionId,
  'query_key': r.queryKey,
  'generation': r.generation,
  'validity_epoch': r.validityEpoch,
  'accessed_at': r.lastAccessAt.microsecondsSinceEpoch,
  'reserved_bytes': OfflineTraversalCodec.reservedBytes(r),
  'payload': _encode(OfflineTraversalCodec.encode(r)),
};
