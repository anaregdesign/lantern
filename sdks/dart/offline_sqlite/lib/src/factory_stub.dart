import 'package:sqflite_common/sqlite_api.dart';

/// Plain Dart callers must explicitly supply their SQLite factory.
DatabaseFactory defaultDatabaseFactory() => throw UnsupportedError(
  'An explicit DatabaseFactory is required outside Flutter.',
);
