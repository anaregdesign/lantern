import 'dart:async';

import 'package:lantern_client/lantern_client.dart';

import 'automatic_owner.dart';
import 'errors.dart';
import 'memory_store.dart';
import 'store.dart';
import 'types.dart';

/// Supported bounded volatile profile over the shared conformance engine.
/// It retains no credentials and makes no crash durability claim.
final class MemoryOfflineStore implements OfflineStore, OfflineAutomaticStore {
  /// Fixes capacity before any admission; transitions reserve their full lifecycle.
  MemoryOfflineStore({OfflineStoreLimits limits = const OfflineStoreLimits()})
    : _engine = InMemoryOfflineStore(limits: limits);
  final InMemoryOfflineStore _engine;
  _MemoryOwner? _owner;
  int _epoch = 0;
  @override
  Object get canonicalOwnerKey => this;
  @override
  Future<OfflineAutomaticOwner> acquireAutomaticOwner({
    required String scope,
    required String bindingFingerprint,
    required String configurationFingerprint,
  }) async {
    if (_owner?.active ?? false) throw const OfflineOwnerConflictException();
    return _owner = _MemoryOwner(++_epoch);
  }

  /// This profile's admission durability is memory only.
  AdmissionDurability get durability => AdmissionDurability.memory;
  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction) action,
  ) => _engine.transaction(action);
  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      _engine.changes(partitionId);
}

final class _MemoryOwner implements OfflineAutomaticOwner {
  _MemoryOwner(this.epoch);
  @override
  final int epoch;
  bool active = true;
  @override
  void checkCurrent() {
    if (!active) throw const OfflineDisposedException();
  }

  @override
  Future<void> release() async {
    active = false;
  }
}
