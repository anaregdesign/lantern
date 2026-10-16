import 'package:lantern_client_offline/src/traversal_clock.dart';
import 'package:test/test.dart';

void main() {
  final t0 = DateTime.utc(2026, 10, 15, 12);
  final t1 = t0.add(const Duration(seconds: 1));
  final t2 = t1.add(const Duration(seconds: 1));
  late DateTime now;
  late OfflineTraversalClock clock;
  late int notifications;
  setUp(() {
    now = t0;
    notifications = 0;
    clock = OfflineTraversalClock(() => now)..onChanged = () => notifications++;
  });

  for (final advanced in [false, true]) {
    test('C1 delayed monotone observation, advanced=$advanced', () {
      final capture = clock.capture();
      now = advanced ? t1 : t0;
      clock.sample();
      clock.reconcile(capture, t0);
      expect(clock.highWater, now);
      expect(clock.held, isFalse);
      expect(clock.phase, 0);
      expect(clock.rollbackRevision, 0);
      expect(clock.invalidationPending, isFalse);
      expect(clock.eligible, isTrue);
      expect(notifications, 0);
    });
  }

  test('C2 old durable observation cannot recover a newer hold', () {
    now = t1;
    final capture = clock.capture();
    now = t0;
    clock.sample();
    final revision = clock.rollbackRevision, phase = clock.phase;
    clock.reconcile(capture, t1);
    expect(clock.highWater, t1);
    expect(clock.held, isTrue);
    expect(clock.rollbackRevision, revision);
    expect(clock.phase, phase);
    expect(clock.invalidationPending, isTrue);
    expect(clock.eligible, isFalse);
  });

  test('C3 recovery during await retains real rollback debt and fence', () {
    now = t1;
    final capture = clock.capture();
    final attemptPhase = clock.phase;
    now = t0;
    clock.sample();
    final revision = clock.rollbackRevision;
    now = t2;
    clock.sample();
    clock.reconcile(capture, t1);
    expect(clock.highWater, t2);
    expect(clock.held, isFalse);
    expect(clock.phase, isNot(attemptPhase));
    expect(clock.rollbackRevision, revision);
    expect(clock.invalidationPending, isTrue);
    expect(clock.eligible, isFalse);
    clock.committed(revision);
    expect(clock.eligible, isTrue);
  });

  for (final recovered in [false, true]) {
    test('C4 discovered persisted barrier, recovered=$recovered', () {
      final capture = clock.capture();
      final phase = clock.phase;
      now = recovered ? t2 : t0;
      clock.reconcile(capture, t2);
      expect(clock.highWater, t2);
      expect(clock.held, !recovered);
      expect(clock.phase, isNot(phase));
      expect(clock.invalidationPending, isTrue);
      expect(clock.eligible, isFalse);
      final revision = clock.rollbackRevision;
      final lower = clock.capture();
      clock.reconcile(lower, t1);
      expect(clock.highWater, t2);
      expect(clock.rollbackRevision, revision);
      expect(clock.invalidationPending, isTrue);
      expect(clock.held, !recovered);
      now = t2;
      clock.sample();
      expect(clock.eligible, isFalse);
      clock.committed(revision);
      expect(clock.eligible, isTrue);
    });
  }
}
