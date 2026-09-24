part of '../lantern_client_offline_sqlite.dart';

const _schemaVersion = 1;
const _applicationId = 0x4c4e544f;
const _fifo = 'ordinal, item_index, record_id';
const _afterFifo =
    '(ordinal>? OR (ordinal=? AND item_index>?) OR '
    '(ordinal=? AND item_index=? AND record_id>?))';
List<Object?> _afterFifoArgs(int ordinal, int itemIndex, String recordId) => [
  ordinal,
  ordinal,
  itemIndex,
  ordinal,
  itemIndex,
  recordId,
];
String _deadlineState(String column) => column == 'dead_lettered_at'
    ? "state='deadLetter'"
    : "state IN ('enqueued','sending')";
const _tables = <String>[
  '''CREATE TABLE cdc_origins (
    partition_id TEXT NOT NULL, origin TEXT NOT NULL, completed TEXT NOT NULL,
    pending TEXT, next_chunk INTEGER NOT NULL, PRIMARY KEY(partition_id, origin),
    FOREIGN KEY(partition_id) REFERENCES partitions(partition_id)) WITHOUT ROWID''',
  '''CREATE TABLE store_metadata (
    key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID''',
  '''CREATE TABLE partitions (
    partition_id TEXT PRIMARY KEY, generation INTEGER NOT NULL,
    next_ordinal INTEGER NOT NULL, version INTEGER NOT NULL,
    auth_paused INTEGER NOT NULL CHECK(auth_paused IN (0,1))) WITHOUT ROWID''',
  '''CREATE TABLE cache (
    partition_id TEXT NOT NULL, entity_key TEXT NOT NULL,
    generation INTEGER NOT NULL, accessed_at INTEGER NOT NULL, vertex_key BLOB,
    reserved_bytes INTEGER NOT NULL, payload BLOB NOT NULL,
    PRIMARY KEY(partition_id, entity_key),
    FOREIGN KEY(partition_id) REFERENCES partitions(partition_id)) WITHOUT ROWID''',
  '''CREATE TABLE outbox (
    partition_id TEXT NOT NULL, record_id TEXT NOT NULL,
    operation_id TEXT NOT NULL, item_index INTEGER NOT NULL,
    entity_key TEXT NOT NULL, generation INTEGER NOT NULL,
    ordinal INTEGER NOT NULL, state TEXT NOT NULL,
    enqueued_at INTEGER NOT NULL, expiration INTEGER,
    next_attempt_at INTEGER, lease_until INTEGER,
    dead_lettered_at INTEGER, reserved_bytes INTEGER NOT NULL,
    payload BLOB NOT NULL, PRIMARY KEY(partition_id, record_id),
    FOREIGN KEY(partition_id) REFERENCES partitions(partition_id)) WITHOUT ROWID''',
  '''CREATE TABLE operations (
    partition_id TEXT NOT NULL, operation_id TEXT NOT NULL,
    generation INTEGER NOT NULL, updated_at INTEGER NOT NULL,
    terminal_at INTEGER, reserved_bytes INTEGER NOT NULL, payload BLOB NOT NULL,
    PRIMARY KEY(partition_id, operation_id),
    FOREIGN KEY(partition_id) REFERENCES partitions(partition_id)) WITHOUT ROWID''',
  '''CREATE TABLE operation_items (
    partition_id TEXT NOT NULL, record_id TEXT NOT NULL,
    operation_id TEXT NOT NULL, item_index INTEGER NOT NULL,
    PRIMARY KEY(partition_id, record_id),
    UNIQUE(partition_id, operation_id, item_index),
    FOREIGN KEY(partition_id, operation_id)
      REFERENCES operations(partition_id, operation_id) ON DELETE CASCADE) WITHOUT ROWID''',
];

final _indexes = <String, String>{
  'cache_vertex_prefix': 'cache(partition_id, vertex_key)',
  'cache_lru': 'cache(accessed_at, partition_id, entity_key)',
  'cache_partition_lru': 'cache(partition_id, accessed_at, entity_key)',
  'outbox_fifo': 'outbox(partition_id, $_fifo)',
  'outbox_operation': 'outbox(partition_id, operation_id, $_fifo)',
  'outbox_entity': 'outbox(partition_id, entity_key, $_fifo)',
  for (final column in ['expiration', 'enqueued_at', 'dead_lettered_at']) ...{
    'outbox_$column':
        'outbox(partition_id, $column, $_fifo) WHERE ${_deadlineState(column)}',
    'outbox_operation_$column':
        'outbox(partition_id, operation_id, $column, $_fifo) WHERE ${_deadlineState(column)}',
    'outbox_entity_$column':
        'outbox(partition_id, entity_key, $column, $_fifo) WHERE ${_deadlineState(column)}',
  },
  'operation_retention': 'operations(terminal_at, partition_id, operation_id)',
  'operation_partition_retention':
      'operations(partition_id, terminal_at, operation_id)',
  'operation_updates': 'operations(partition_id, updated_at, operation_id)',
};

Future<void> _createSchema(Database db, int version) async {
  final existing = await db.rawQuery(
    "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name != 'android_metadata'",
  );
  if (existing.isNotEmpty) throw const OfflineSchemaException();
  for (final statement in _tables) {
    await db.execute(statement);
  }
  for (final entry in _indexes.entries) {
    await db.execute('CREATE INDEX ${entry.key} ON ${entry.value}');
  }
  await db.insert('store_metadata', {
    'key': 'format',
    'value': 'lantern-offline-1',
  });
  await db.execute('PRAGMA application_id = $_applicationId');
}

Future<void> _checkSchema(Database db) async {
  try {
    final mode = (await db.rawQuery(
      'PRAGMA journal_mode',
    )).single.values.single;
    final sync = (await db.rawQuery('PRAGMA synchronous')).single.values.single;
    final foreignKeys = (await db.rawQuery(
      'PRAGMA foreign_keys',
    )).single.values.single;
    final files = await db.rawQuery('PRAGMA database_list');
    if (mode == 'off' ||
        mode == 'memory' ||
        sync is! int ||
        sync < 2 ||
        foreignKeys != 1 ||
        !files.any(
          (row) =>
              row['name'] == 'main' &&
              row['file'] is String &&
              (row['file']! as String).isNotEmpty,
        )) {
      throw const OfflineSchemaException();
    }
    final id = (await db.rawQuery(
      'PRAGMA application_id',
    )).single.values.single;
    final version = await db.getVersion();
    final marker = await db.query('store_metadata');
    if (id != _applicationId ||
        version != _schemaVersion ||
        marker.length != 1 ||
        marker.single['key'] != 'format' ||
        marker.single['value'] != 'lantern-offline-1') {
      throw const OfflineSchemaException();
    }
    final definitions = <String, String>{
      for (final statement in _tables)
        RegExp(r'CREATE TABLE (\w+)').firstMatch(statement)!.group(1)!:
            statement,
      for (final entry in _indexes.entries)
        entry.key: 'CREATE INDEX ${entry.key} ON ${entry.value}',
    };
    final objects = await db.rawQuery(
      "SELECT name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' AND name != 'android_metadata'",
    );
    String normalize(String sql) => sql.replaceAllMapped(
      RegExp(r"'[^']*(?:''[^']*)*'|\s+"),
      (match) => match[0]!.startsWith("'") ? match[0]! : '',
    );
    if (objects.length != definitions.length) {
      throw const OfflineSchemaException();
    }
    for (final object in objects) {
      final expected = definitions[object['name']];
      final actual = object['sql'];
      if (expected == null ||
          actual is! String ||
          normalize(actual) != normalize(expected)) {
        throw const OfflineSchemaException();
      }
    }
    final integrity = await db.rawQuery('PRAGMA quick_check(1)');
    if (integrity.length != 1 ||
        integrity.single.values.single != 'ok' ||
        (await db.rawQuery('PRAGMA foreign_key_check')).isNotEmpty) {
      throw const OfflineSchemaException();
    }
  } on OfflineSchemaException {
    rethrow;
  } on DatabaseException catch (error, stack) {
    Error.throwWithStackTrace(_safeDatabaseError(error), stack);
  } catch (_) {
    throw const OfflineSchemaException();
  }
}
