import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:path/path.dart' as path;
import 'package:sqflite/sqflite.dart' as sqflite;

/// Stable database filename bound to the application endpoint and account scope.
///
/// A scope is an opaque, non-secret user/tenant identifier supplied by the app;
/// credentials never belong in a database path or partition identifier.
String offlineDatabaseFileName(Uri endpoint, String partitionId) {
  if (partitionId.trim().isEmpty) {
    throw ArgumentError.value(partitionId, 'partitionId', 'must not be empty');
  }
  final identity = utf8.encode(jsonEncode([endpoint.toString(), partitionId]));
  return 'lantern-offline-${sha256.convert(identity)}.db';
}

/// Owns a real SQLite store for one app session, with ordered shutdown.
final class OfflineDemoSession {
  OfflineDemoSession._(this.store, this.repository, this.partitionId);

  final SqliteOfflineStore store;
  final OfflineLanternRepository repository;
  final String partitionId;
  Future<void>? _closing;

  static Future<OfflineDemoSession> open({
    required LanternClient client,
    required Uri endpoint,
    required String partitionId,
  }) async {
    final filename = offlineDatabaseFileName(endpoint, partitionId);
    final directory = await sqflite.getDatabasesPath();
    final store = await SqliteOfflineStore.open(
      path: path.join(directory, filename),
    );
    return OfflineDemoSession._(
      store,
      OfflineLanternRepository(
        store: store,
        remote: LanternClientOfflineRemote(client),
      ),
      partitionId,
    );
  }

  /// Await before replacing an authenticated session or discarding credentials.
  Future<void> wipeOnLogout() => repository.wipePartition(partitionId);

  /// Quiesce repository work before closing the native database connection.
  Future<void> close() => _closing ??= _close();

  Future<void> _close() async {
    await repository.dispose();
    await store.close();
  }
}
