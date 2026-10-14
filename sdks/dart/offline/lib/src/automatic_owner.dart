/// Optional capability required before a later automatic runtime may start.
/// Store transactions/item leases alone do not prove an exclusive lifetime owner.
abstract interface class OfflineAutomaticStore {
  /// Canonical backing-store identity, resolving aliases in durable adapters.
  Object get canonicalOwnerKey;

  /// Acquires one process/isolate owner under an immutable binding/configuration.
  /// A conflicting live owner must fail before any network work. Matching handles
  /// are reference-counted by the runtime, not by independently starting workers.
  Future<OfflineAutomaticOwner> acquireAutomaticOwner({
    required String scope,
    required String bindingFingerprint,
    required String configurationFingerprint,
  });
}

/// Exclusive owned lifetime furnished by a qualified adapter.
abstract interface class OfflineAutomaticOwner {
  /// Monotone acquisition identity; callbacks capture and compare this value.
  int get epoch;

  /// Throws when this lifetime no longer owns the backing store.
  void checkCurrent();

  /// Releases only after the runtime canceled and joined all owned work.
  Future<void> release();
}
