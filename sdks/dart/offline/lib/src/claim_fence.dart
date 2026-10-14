import 'dart:async';

import 'errors.dart';
import 'types.dart';

/// Optional adapter capability needed by qualified common mutation delivery.
/// The fence observes committed claim/time changes synchronously in the store's
/// supported coordination scope. It is not exclusive runtime ownership.
abstract interface class OfflineStoreClaimTransaction {
  /// Captures a sending claim. It becomes usable only after this transaction
  /// commits, and must reject an expired/replaced claim before send/publication.
  FutureOr<OfflineClaimFence> captureClaimFence(
    OfflineOutboxRecord claim, {
    required String authorityBinding,
  });
}

/// Short-lived claim eligibility, independent of a durable result transaction.
/// Adapters update it before publishing a successful commit, including quiet
/// high-water changes. A storage failure does not fabricate a claim replacement.
final class OfflineClaimFence {
  /// Adapter-owned committed state supplies the synchronous predicate.
  OfflineClaimFence({
    required DateTime Function(DateTime) checkCurrent,
    void Function()? onRelease,
  }) : _check = checkCurrent,
       _onRelease = onRelease;
  final DateTime Function(DateTime) _check;
  final void Function()? _onRelease;
  bool _released = false;

  /// Checks the original claim and returns max(observed time, committed high-water).
  DateTime checkCurrent(DateTime observedAt) {
    if (_released) throw const OfflineClaimLostException();
    return _check(observedAt.toUtc());
  }

  /// Releases this pass's bounded registration; idempotent.
  void release() {
    if (_released) return;
    _released = true;
    _onRelease?.call();
  }
}

/// Shared adapter claim predicate; all fields come from committed storage.
DateTime checkOfflineClaim({
  required OfflineOutboxRecord expected,
  required OfflineOutboxRecord? current,
  required int generation,
  required String? authorityBinding,
  required String expectedAuthority,
  required DateTime highWater,
  required DateTime observedAt,
}) {
  final now = observedAt.isBefore(highWater) ? highWater : observedAt;
  if (current == null ||
      current.state != OfflineOutboxState.sending ||
      current.generation != expected.generation ||
      generation != expected.generation ||
      authorityBinding != expectedAuthority ||
      current.recordId != expected.recordId ||
      current.operationId != expected.operationId ||
      current.leaseOwner != expected.leaseOwner ||
      current.leaseUntil != expected.leaseUntil ||
      current.leaseUntil == null ||
      !now.isBefore(current.leaseUntil!)) {
    throw const OfflineClaimLostException();
  }
  return now;
}
