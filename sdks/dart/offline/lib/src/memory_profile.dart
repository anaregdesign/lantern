import 'dart:async';
import 'package:lantern_client/lantern_client.dart';
import 'memory_store.dart';
import 'store.dart';
import 'types.dart';

/// Supported bounded volatile profile over the shared conformance engine.
/// It retains no credentials and makes no crash durability claim.
final class MemoryOfflineStore implements OfflineStore {
  /// Fixes capacity before any admission; transitions reserve their full lifecycle.
  MemoryOfflineStore({OfflineStoreLimits limits = const OfflineStoreLimits()})
    : _engine = InMemoryOfflineStore(limits: limits);
  final InMemoryOfflineStore _engine;

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
