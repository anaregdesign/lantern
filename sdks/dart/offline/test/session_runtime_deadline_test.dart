import 'dart:async';

import 'package:lantern_client_offline/src/session_runtime.dart';
import 'package:test/test.dart';

import 'support/session_clock.dart';
import 'support/session_owner.dart';

void main() {
  late Clock clock;
  late OfflineSessionBudget budget;
  late OfflineSessionRuntime runtime;
  late List<Object> errors;
  DateTime? advanceAfterSample, advanceOnCompletion;
  var clockReads = 0;

  setUp(() {
    clock = Clock();
    budget = OfflineSessionBudget();
    errors = [];
    advanceAfterSample = advanceOnCompletion = null;
    clockReads = 0;
    runtime = OfflineSessionRuntime(
      owner: TestOwner(),
      budget: budget,
      clock: () {
        clockReads++;
        final sampled = clock.now;
        if (advanceAfterSample case final next?) {
          advanceAfterSample = null;
          clock.now = next;
        }
        return sampled;
      },
      schedule: clock.schedule,
      fence: () {},
      onError: (error, _) => errors.add(error),
      onNetworkActivity: () {
        if (runtime.networkInFlightCount == 0 && advanceOnCompletion != null) {
          clock.now = advanceOnCompletion!;
          advanceOnCompletion = null;
        }
      },
      maxSends: 1,
    );
  });
  tearDown(() async {
    await runtime.close();
    expect(runtime.jobCount, 0);
    expect(runtime.inFlightCount, 0);
    expect(runtime.hasWakeup, isFalse);
    expect(clock.active, 0);
    expect(errors, isEmpty);
  });

  void fireEarly(DateTime cutoff, DateTime nextSample) {
    final alarm = clock.alarms.last;
    expect(alarm.canceled, isFalse);
    alarm.canceled = true;
    clock.callbacks++;
    clock.now = cutoff;
    advanceAfterSample = nextSample;
    alarm.callback();
  }

  for (final boundary in ['job', 'network', 'both']) {
    test('$boundary deadline crossing retains one automatic wakeup', () async {
      final due = clock.now.add(const Duration(milliseconds: 100));
      var runs = 0;
      if (boundary != 'job') runtime.deferNetworkUntil(due);
      runtime.enqueue(
        'retry',
        OfflineSessionLane.reconcile,
        (_) async {
          runs++;
        },
        dueAt: boundary == 'network' ? clock.now : due,
      );
      fireEarly(due.subtract(const Duration(microseconds: 1)), due);
      expect(runs, 0);
      expect(clock.active, 1);
      expect(clock.alarms.last.at, due);
      await clock.advance(Duration.zero);
      expect(runs, 1);
      expect(clock.callbacks, 2);
      expect(runtime.jobCount, 0);
      expect(runtime.hasWakeup, isFalse);
    });
  }

  test('early alarm retains the later network floor without sending', () async {
    final due = clock.now.add(const Duration(milliseconds: 100));
    var runs = 0;
    runtime.deferNetworkUntil(due);
    runtime.enqueue(
      'retry',
      OfflineSessionLane.send,
      (_) async {
        runs++;
      },
      dueAt: due.subtract(const Duration(milliseconds: 50)),
    );
    fireEarly(
      due.subtract(const Duration(microseconds: 2)),
      due.subtract(const Duration(microseconds: 1)),
    );
    await flush();
    expect(runs, 0);
    expect(clock.active, 1);
    expect(clock.alarms.last.at, due);
    expect(clock.callbacks, 1);
    await clock.advance(const Duration(microseconds: 1));
    expect(runs, 1);
    expect(clock.callbacks, 2);
  });

  for (final global in [false, true]) {
    test(
      '${global ? 'global' : 'scope'} permit wait does not spin after crossing',
      () async {
        final held = Completer<void>();
        addTearDown(() {
          if (!held.isCompleted) held.complete();
        });
        final order = <String>[];
        final otherClock = Clock();
        // Charge every global send permit in the global case. The runtime under
        // test itself has only one scoped permit, independent of the global cap.
        final other = OfflineSessionRuntime(
          owner: TestOwner(),
          budget: budget,
          clock: otherClock.call,
          schedule: otherClock.schedule,
          fence: () {},
          onError: (error, _) => errors.add(error),
          maxSends: budget.maxSends,
        );
        addTearDown(() async {
          if (!held.isCompleted) held.complete();
          await other.close();
        });
        final holder = global ? other : runtime;
        final holderClock = global ? otherClock : clock;
        for (var i = 0; i < (global ? budget.maxSends : 1); i++) {
          holder.enqueue('held-$i', OfflineSessionLane.send, (_) async {
            await held.future;
          });
        }
        await holderClock.advance(const Duration(milliseconds: 50));
        final due = clock.now.add(const Duration(milliseconds: 100));
        runtime.enqueue('retry', OfflineSessionLane.send, (_) async {
          order.add('retry');
        }, dueAt: due);
        fireEarly(due.subtract(const Duration(microseconds: 1)), due);
        await clock.advance(Duration.zero);
        expect(order, isEmpty);
        expect(runtime.hasWakeup, isFalse);
        final reads = clockReads, callbacks = clock.callbacks;
        await clock.advance(const Duration(seconds: 1));
        expect(clockReads, reads);
        expect(clock.callbacks, callbacks);
        expect(order, isEmpty);
        held.complete();
        await flush();
        expect(order, ['retry']);
        expect(runtime.jobCount, 0);
        expect(runtime.hasWakeup, isFalse);
      },
    );
  }

  test(
    'flight completion already schedules work whose deadline just crossed',
    () async {
      final held = Completer<void>();
      addTearDown(() {
        if (!held.isCompleted) held.complete();
      });
      final order = <String>[];
      runtime.enqueue('held', OfflineSessionLane.send, (_) async {
        order.add('held');
        await held.future;
      });
      await clock.advance(const Duration(milliseconds: 50));
      final due = clock.now.add(const Duration(milliseconds: 100));
      runtime.enqueue('next', OfflineSessionLane.send, (_) async {
        order.add('next');
      }, dueAt: due);
      clock.now = due.subtract(const Duration(microseconds: 1));
      advanceOnCompletion = due;
      held.complete();
      await flush();
      expect(order, ['held', 'next']);
      expect(clock.callbacks, 1);
      expect(runtime.jobCount, 0);
      expect(runtime.hasWakeup, isFalse);
    },
  );

  test(
    'local deadline crossing progresses while network stays paused',
    () async {
      await runtime.pauseNetwork();
      final due = clock.now.add(const Duration(milliseconds: 100));
      runtime.deferNetworkUntil(due.add(const Duration(days: 1)));
      var locals = 0, sends = 0;
      runtime.enqueue('send', OfflineSessionLane.send, (_) async {
        sends++;
      }, dueAt: due);
      runtime.enqueue('expiry', OfflineSessionLane.local, (_) async {
        locals++;
      }, dueAt: due);
      fireEarly(due.subtract(const Duration(microseconds: 1)), due);
      await clock.advance(Duration.zero);
      expect(locals, 1);
      expect(sends, 0);
      expect(runtime.jobCount, 1);
      expect(runtime.hasWakeup, isFalse);
      expect(clock.callbacks, 2);
    },
  );
}
