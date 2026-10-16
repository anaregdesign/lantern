import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import '../../offline/tool/automatic_diagnostics.dart';

/// Passive measurements of calls already made by the fixture's adapter.
final class SqlProbeProfile {
  SqlProbeProfile(this.diagnostics);
  final AutomaticDiagnostics diagnostics;
  final Map<String, ProbeHistogram> groups = {};
  int callsInFlight = 0, transactionsInFlight = 0, commits = 0, rollbacks = 0;
  int? lastProgressMicros, terminalCommitMicros;

  void record(String category, int duration, String phase) {
    (groups['$phase/$category'] ??= ProbeHistogram()).add(duration);
    lastProgressMicros = diagnostics.micros();
  }

  Future<T> call<T>(String category, Future<T> Function() action) async {
    final started = diagnostics.micros(), phase = diagnostics.phase;
    callsInFlight++;
    try {
      return await action();
    } finally {
      callsInFlight--;
      record(category, diagnostics.micros() - started, phase);
    }
  }

  Map<String, Object?> snapshot() => {
    'callsInFlight': callsInFlight,
    'transactionsInFlight': transactionsInFlight,
    'commits': commits,
    'rollbacks': rollbacks,
    'lastProgressMicros': lastProgressMicros,
    'terminalCommitMicros': terminalCommitMicros,
    'groups': {for (final e in groups.entries) e.key: e.value.snapshot()},
    'commitTailMeaning':
        'body exit to native transaction completion; not pure fsync',
  };
}

String _table(String value) =>
    const {
      'operations',
      'operation_items',
      'operation_dependencies',
      'outbox',
      'partitions',
      'cache',
      'traversals',
      'recovery',
      'automatic_owner',
      'store_metadata',
      'cdc_scoped',
      'cdc_origins',
    }.contains(value)
    ? value
    : 'other';

String _rawCategory(String sql) {
  final normalized = sql.trim().toLowerCase();
  if (normalized.startsWith('savepoint') ||
      normalized.startsWith('release savepoint') ||
      normalized.startsWith('rollback to savepoint')) {
    return 'savepoint';
  }
  if (normalized.startsWith('pragma')) return 'pragma';
  final table = RegExp(
    r'\b(?:from|into|update)\s+([a-z_]+)',
  ).firstMatch(normalized)?.group(1);
  return _table(table ?? 'other');
}

mixin _MeasuredExecutor implements DatabaseExecutor {
  DatabaseExecutor get delegate;
  SqlProbeProfile get profile;
  void written(String table, Map<String, Object?> values) {}
  void rolledBackSavepoint() {}

  @override
  Future<List<Map<String, Object?>>> query(
    String table, {
    bool? distinct,
    List<String>? columns,
    String? where,
    List<Object?>? whereArgs,
    String? groupBy,
    String? having,
    String? orderBy,
    int? limit,
    int? offset,
  }) => profile.call(
    'query/${_table(table)}',
    () => delegate.query(
      table,
      distinct: distinct,
      columns: columns,
      where: where,
      whereArgs: whereArgs,
      groupBy: groupBy,
      having: having,
      orderBy: orderBy,
      limit: limit,
      offset: offset,
    ),
  );

  @override
  Future<List<Map<String, Object?>>> rawQuery(
    String sql, [
    List<Object?>? arguments,
  ]) => profile.call(
    'rawQuery/${_rawCategory(sql)}',
    () => delegate.rawQuery(sql, arguments),
  );

  @override
  Future<int> insert(
    String table,
    Map<String, Object?> values, {
    String? nullColumnHack,
    ConflictAlgorithm? conflictAlgorithm,
  }) async {
    final result = await profile.call(
      'insert/${_table(table)}',
      () => delegate.insert(
        table,
        values,
        nullColumnHack: nullColumnHack,
        conflictAlgorithm: conflictAlgorithm,
      ),
    );
    written(table, values);
    return result;
  }

  @override
  Future<int> update(
    String table,
    Map<String, Object?> values, {
    String? where,
    List<Object?>? whereArgs,
    ConflictAlgorithm? conflictAlgorithm,
  }) async {
    final result = await profile.call(
      'update/${_table(table)}',
      () => delegate.update(
        table,
        values,
        where: where,
        whereArgs: whereArgs,
        conflictAlgorithm: conflictAlgorithm,
      ),
    );
    if (result > 0) written(table, values);
    return result;
  }

  @override
  Future<int> delete(String table, {String? where, List<Object?>? whereArgs}) =>
      profile.call(
        'delete/${_table(table)}',
        () => delegate.delete(table, where: where, whereArgs: whereArgs),
      );
  @override
  Future<int> rawInsert(String sql, [List<Object?>? arguments]) => profile.call(
    'rawInsert/${_rawCategory(sql)}',
    () => delegate.rawInsert(sql, arguments),
  );
  @override
  Future<int> rawUpdate(String sql, [List<Object?>? arguments]) => profile.call(
    'rawUpdate/${_rawCategory(sql)}',
    () => delegate.rawUpdate(sql, arguments),
  );
  @override
  Future<int> rawDelete(String sql, [List<Object?>? arguments]) => profile.call(
    'rawDelete/${_rawCategory(sql)}',
    () => delegate.rawDelete(sql, arguments),
  );
  @override
  Future<void> execute(String sql, [List<Object?>? arguments]) async {
    await profile.call(
      'execute/${_rawCategory(sql)}',
      () => delegate.execute(sql, arguments),
    );
    if (sql.trim().toUpperCase().startsWith('ROLLBACK TO')) {
      rolledBackSavepoint();
    }
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

final class ProfileDatabase with _MeasuredExecutor implements Database {
  ProfileDatabase(this.delegate, this.profile);
  @override
  final Database delegate;
  @override
  final SqlProbeProfile profile;
  @override
  String get path => delegate.path;
  @override
  bool get isOpen => delegate.isOpen;
  @override
  Future<void> close() => delegate.close();
  @override
  Future<T> transaction<T>(
    Future<T> Function(Transaction) action, {
    bool? exclusive,
  }) async {
    final started = profile.diagnostics.micros(),
        phase = profile.diagnostics.phase;
    int? entered, exited;
    _ProfileTransaction? measured;
    profile.transactionsInFlight++;
    try {
      final result = await delegate.transaction((tx) async {
        entered = profile.diagnostics.micros();
        profile.record('transaction/entry', entered! - started, phase);
        measured = _ProfileTransaction(tx, profile);
        try {
          return await action(measured!);
        } finally {
          exited = profile.diagnostics.micros();
          profile.record('transaction/body', exited! - entered!, phase);
        }
      }, exclusive: exclusive);
      profile.commits++;
      // This fresh probe has one aggregate; never infer commit from staged SQL.
      if (measured?.terminalStaged == true) {
        profile.terminalCommitMicros ??= profile.diagnostics.micros();
      }
      return result;
    } catch (_) {
      profile.rollbacks++;
      rethrow;
    } finally {
      profile.transactionsInFlight--;
      final end = profile.diagnostics.micros();
      profile.record('transaction/total', end - started, phase);
      if (exited != null) {
        profile.record('transaction/completionTail', end - exited!, phase);
      }
    }
  }
}

final class _ProfileTransaction with _MeasuredExecutor implements Transaction {
  _ProfileTransaction(this.delegate, this.profile);
  @override
  final Transaction delegate;
  @override
  final SqlProbeProfile profile;
  bool terminalStaged = false;
  @override
  void written(String table, Map<String, Object?> values) {
    if (table == 'operations' && values.containsKey('terminal_at')) {
      terminalStaged = values['terminal_at'] != null;
    }
  }

  @override
  void rolledBackSavepoint() => terminalStaged = false;
}
