// Copyright 2026 anaregdesign. Licensed under the Apache License, Version 2.0.
part of '../lantern_client_offline_sqlite.dart';

final class _SqlAutomaticOwner implements OfflineAutomaticOwner {
  _SqlAutomaticOwner(this.store, this.guard, this.epoch, this.token);
  final SqliteOfflineStore store;
  final NativeOfflineOwner guard;
  @override
  final int epoch;
  final String token;
  bool active = true;
  Future<void>? _releasing;

  @override
  void checkCurrent() {
    if (!active || store._closed) throw const OfflineDisposedException();
    guard.check();
  }

  Future<void> checkTransaction(DatabaseExecutor db) async {
    checkCurrent();
    final rows = await db.query('automatic_owner', where: 'id=1');
    checkCurrent();
    if (rows.length != 1 ||
        rows.single['epoch'] != epoch ||
        rows.single['token'] != token ||
        rows.single['active'] != 1) {
      active = false;
      throw const OfflineDisposedException();
    }
  }

  @override
  Future<void> release() {
    if (_releasing != null) return _releasing!;
    // The factory joins network work before this call. Invalidate immediately,
    // then join accepted DB callbacks before returning only our row token.
    active = false;
    return _releasing = store._lane.run(() async {
      try {
        await _databaseCall(
          () => store._db.transaction((sql) async {
            await sql.update(
              'automatic_owner',
              {'active': 0},
              where: 'id=1 AND epoch=? AND token=?',
              whereArgs: [epoch, token],
            );
          }, exclusive: true),
        );
      } finally {
        // Row cleanup failure is not an ownership proof. A new OS guard may
        // adopt that stale row; never leave the old work capability usable.
        guard.release();
      }
    });
  }
}
