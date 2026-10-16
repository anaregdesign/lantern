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

  /// Attempt phase, changed on hold, recovery or discovered historical rollback.
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
    final nextHeld = raw.isBefore(highWater);
    if (nextHeld != held) {
      held = nextHeld;
      phase++;
      if (held) rollbackRevision++;
      onChanged?.call();
    }
    if (raw.isAfter(highWater)) highWater = raw;
    return raw;
  }

  /// Captures one durable lookup's raw input and already-known barrier together.
  ({DateTime raw, DateTime highWater}) capture() {
    final raw = sample();
    return (raw: raw, highWater: highWater);
  }

  /// Consumes that lookup's capture once, without replaying old raw time as now.
  void reconcile(
    ({DateTime raw, DateTime highWater}) capture,
    DateTime durableHighWater,
  ) {
    final historicalRollback = durableHighWater.isAfter(capture.highWater);
    if (durableHighWater.isAfter(highWater)) highWater = durableHighWater;
    final revision = rollbackRevision;
    sample();
    if (historicalRollback && rollbackRevision == revision) {
      // Recovery during the await does not erase a newly discovered past hold.
      // This knowledge survives rollback of the surrounding store transaction;
      // only a successful invalidation commit may acknowledge it.
      rollbackRevision++;
      phase++;
      onChanged?.call();
    }
  }

  /// Acknowledges only a successful outer transaction, never its callback.
  void committed(int revision) {
    if (revision > _committed) _committed = revision;
  }
}
