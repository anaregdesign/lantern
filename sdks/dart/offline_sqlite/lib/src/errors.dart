part of '../lantern_client_offline_sqlite.dart';

/// A native SQLite failure stripped of SQL, paths, keys, values, and arguments.
///
/// User callback exceptions are preserved. Native database failures alone are
/// translated so logging this exception cannot disclose stored application data.
final class SqliteOfflineStoreException implements Exception {
  SqliteOfflineStoreException._(this.code, this.resultCode);

  /// Stable content-free category such as `sqlite_full` or `sqlite_busy`.
  final String code;

  /// SQLite's numeric extended result code, when supplied by the platform.
  final int? resultCode;

  @override
  String toString() => 'SqliteOfflineStoreException($code, code=$resultCode)';
}

Object _safeDatabaseError(Object error) {
  if (error is! DatabaseException) return error;
  int? result;
  try {
    result = error.getResultCode();
  } catch (_) {
    /* No native text escapes. */
  }
  final code = switch (result == null ? null : result & 255) {
    5 || 6 => 'sqlite_busy',
    8 => 'sqlite_read_only',
    10 => 'sqlite_io',
    11 || 26 => 'sqlite_corrupt',
    13 => 'sqlite_full',
    14 => 'sqlite_open',
    19 => 'sqlite_constraint',
    _ => error.isDatabaseClosedError() ? 'sqlite_closed' : 'sqlite_failure',
  };
  return SqliteOfflineStoreException._(code, result);
}

Future<T> _databaseCall<T>(Future<T> Function() action) async {
  try {
    return await action();
  } on DatabaseException catch (error, stack) {
    Error.throwWithStackTrace(_safeDatabaseError(error), stack);
  }
}
