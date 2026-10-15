// Copyright 2026 anaregdesign. Licensed under the Apache License, Version 2.0.
part of '../lantern_client_offline_sqlite.dart';

const _installationTable = '''CREATE TABLE installation_binding (
  id INTEGER PRIMARY KEY CHECK(id=1), format INTEGER NOT NULL CHECK(format=1),
  installation_id TEXT NOT NULL CHECK(length(installation_id) BETWEEN 1 AND 256),
  policy_id TEXT NOT NULL CHECK(length(policy_id) BETWEEN 1 AND 256),
  policy_version INTEGER NOT NULL CHECK(policy_version BETWEEN 1 AND 2147483647))''';
const _adoptionTable = '''CREATE TABLE scope_adoptions (
  destination_partition TEXT PRIMARY KEY,
  source_partition TEXT NOT NULL UNIQUE CHECK(length(source_partition) BETWEEN 1 AND 4096),
  format INTEGER NOT NULL CHECK(format=1),
  installation_id TEXT NOT NULL, policy_id TEXT NOT NULL, policy_version INTEGER NOT NULL,
  FOREIGN KEY(destination_partition) REFERENCES partitions(partition_id)) WITHOUT ROWID''';

Future<void> _addInstallationSchema(DatabaseExecutor db) async {
  await db.execute(_installationTable);
  await db.execute(_adoptionTable);
}

Future<void> _adoptScope(
  DatabaseExecutor sql,
  _SqlTransaction tx,
  SqliteScopeAdoption mapping,
  Map<String, Object?> marker,
  OfflineStoreLimits limits,
) async {
  final source = mapping.sourcePartition, target = mapping.destinationPartition;
  final from = await sql.query(
    'partitions',
    where: 'partition_id=?',
    whereArgs: [source],
  );
  final to = await sql.query(
    'partitions',
    where: 'partition_id=?',
    whereArgs: [target],
  );
  final ledger = await sql.query(
    'scope_adoptions',
    where: 'source_partition=? OR destination_partition=?',
    whereArgs: [source, target],
  );
  final record = <String, Object?>{
    'destination_partition': target,
    'source_partition': source,
    'format': 1,
    'installation_id': marker['installation_id'],
    'policy_id': marker['policy_id'],
    'policy_version': marker['policy_version'],
  };
  if (from.isEmpty &&
      to.length == 1 &&
      ledger.length == 1 &&
      record.keys.every((key) => ledger.single[key] == record[key])) {
    // The caller already validated the whole target graph. An uncertain prior
    // commit is resolved by this ledger, without replaying application writes.
    return;
  }
  if (from.length != 1 || to.isNotEmpty || ledger.isNotEmpty) {
    throw const SqliteStartupException(SqliteStartupFailure.scopeConflict);
  }
  // Reset granting views using the existing recovery semantics before copying
  // the scope. Original intent, leases, times, generation and pauses stay intact.
  await tx.resetScopedChangeCursor(source, null);
  await sql.delete('traversals', where: 'partition_id=?', whereArgs: [source]);
  final partition = Map<String, Object?>.of(
    (await sql.query(
      'partitions',
      where: 'partition_id=?',
      whereArgs: [source],
    )).single,
  );
  partition['partition_id'] = target;
  final oldAuthority = partition['authority_binding'];
  partition['authority_binding'] = mapping.authorityBinding;
  // A null legacy binding supplies no evidence that a policy pause is obsolete.
  if (oldAuthority != null && oldAuthority != mapping.authorityBinding) {
    partition['policy_paused'] = 0;
  }
  await sql.insert('partitions', partition);
  await _copyScopeRows(sql, 'operations', source, target, 'operation_id', (
    row,
  ) {
    final r = _operation(row);
    return _operationColumns(
      OfflineOperationRecord(
        partitionId: target,
        generation: r.generation,
        operationId: r.operationId,
        items: r.items,
        updatedAt: r.updatedAt,
        terminalAt: r.terminalAt,
        kind: r.kind,
        guardUntil: r.guardUntil,
        groupId: r.groupId,
      ),
      limits,
    );
  });
  await _copyScopeRows(sql, 'outbox', source, target, 'record_id', (row) {
    final r = _outbox(row);
    return _outboxColumns(
      OfflineOutboxRecord(
        recordId: r.recordId,
        operationId: r.operationId,
        itemIndex: r.itemIndex,
        partitionId: target,
        intent: r.intent,
        enqueuedAt: r.enqueuedAt,
        ordinal: r.ordinal,
        state: r.state,
        attemptCount: r.attemptCount,
        generation: r.generation,
        preparation: r.preparation,
        mayHaveDispatched: r.mayHaveDispatched,
        dependencies: r.dependencies,
        nextAttemptAt: r.nextAttemptAt,
        leaseOwner: r.leaseOwner,
        leaseUntil: r.leaseUntil,
        deadLetteredAt: r.deadLetteredAt,
        diagnosticCode: r.diagnosticCode,
      ),
      limits,
    );
  });
  for (final table in [
    'operation_items',
    'operation_dependencies',
    'recovery',
  ]) {
    // These tables have no embedded partition identity. All column values other
    // than the foreign key are copied exactly, under enabled FK constraints.
    final columns = (await sql.rawQuery(
      'PRAGMA table_info($table)',
    )).map((r) => r['name']! as String).toList();
    await sql.rawInsert(
      'INSERT INTO $table (${columns.join(',')}) '
      'SELECT ${columns.map((c) => c == 'partition_id' ? '?' : c).join(',')} '
      'FROM $table WHERE partition_id=?',
      [target, source],
    );
  }
  for (final table in [
    'operation_dependencies',
    'operation_items',
    'outbox',
    'operations',
    'recovery',
    'cache',
    'cdc_scoped',
    'cdc_origins',
    'partitions',
  ]) {
    await sql.delete(table, where: 'partition_id=?', whereArgs: [source]);
  }
  await sql.insert('scope_adoptions', record);
  // Final validateAll checks the logical graph and recomputed reservations only
  // after the temporary copy has gone. No eviction or partial admission occurs.
}

Future<void> _copyScopeRows(
  DatabaseExecutor sql,
  String table,
  String source,
  String target,
  String key,
  Map<String, Object?> Function(Map<String, Object?>) rewrite,
) async {
  String? cursor;
  while (true) {
    final rows = await sql.query(
      table,
      where: 'partition_id=?${cursor == null ? '' : ' AND $key>?'}',
      whereArgs: [source, ?cursor],
      orderBy: key,
      limit: 128,
    );
    if (rows.isEmpty) return;
    for (final row in rows) {
      final columns = rewrite(row);
      if (columns['partition_id'] != target) {
        throw const OfflineSchemaException();
      }
      await sql.insert(table, columns);
    }
    cursor = rows.last[key]! as String;
  }
}
