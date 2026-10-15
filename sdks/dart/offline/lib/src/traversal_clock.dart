import 'types.dart';

/// Owner-local raw clock seam. Sampling never performs I/O or starts a timer.
final class OfflineTraversalClock {
  /// Retains the raw source independently of the effective CRUD clock.
  OfflineTraversalClock(this.rawClock);

  /// Application clock, sampled once at each boundary.
  final OfflineClock rawClock;

  /// Coalesced owner wakeup notification, never a transaction callback.
  void Function()? onChanged;

  /// Effective high-water, never lowered on recovery.
  DateTime highWater = DateTime.fromMicrosecondsSinceEpoch(0, isUtc: true);

  /// Whether raw wall is currently below the known barrier.
  bool held = false;

  /// Attempt phase, changed on either hold or recovery.
  int phase = 0;

  /// Latest rollback requiring a committed invalidation.
  int rollbackRevision = 0;
  int _committed = 0;

  /// A detected rollback has not yet crossed a durable commit boundary.
  bool get invalidationPending => rollbackRevision != _committed;

  /// New attempts may retain only in an eligible phase.
  bool get eligible => !held && !invalidationPending;

  /// Samples once; immediately latches any observed rollback before returning.
  DateTime sample() {
    final raw = rawClock().toUtc();
    observe(raw, highWater);
    return raw;
  }

  /// Incorporates a transaction's durable high-water for the same raw sample.
  void observe(DateTime raw, DateTime durableHighWater) {
    if (durableHighWater.isAfter(highWater)) highWater = durableHighWater;
    final nextHeld = raw.isBefore(highWater);
    if (nextHeld != held) {
      held = nextHeld;
      phase++;
      if (held) rollbackRevision++;
      onChanged?.call();
    }
    if (raw.isAfter(highWater)) highWater = raw;
  }

  /// Acknowledges only a successful outer transaction, never its callback.
  void committed(int revision) {
    if (revision > _committed) _committed = revision;
  }
}
