import 'dart:async';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/lantern_client_adapter.dart';
import 'package:lantern_client_offline/src/session_runtime.dart';
import 'package:test/test.dart';

import 'support/session_clock.dart';
import 'support/session_owner.dart';

void main() {
  late Clock clock;
  late OfflineSessionRuntime runtime;
  late LanternObservationCoordinator coordinator;
  final errors = <Object>[];
  setUp(() {
    clock = Clock();
    errors.clear();
    runtime = OfflineSessionRuntime(
      owner: TestOwner(),
      budget: OfflineSessionBudget(),
      clock: clock.call,
      schedule: clock.schedule,
      fence: () {},
      onError: (e, _) => errors.add(e),
    );
    coordinator = LanternObservationCoordinator(
      scheduler: runtime.observationScheduler,
    );
  });
  tearDown(() async {
    await coordinator.close();
    await runtime.close();
    expect(errors, isEmpty);
  });

  test(
    'S1 two subscribers share a load; last query cancel joins without canceling writes',
    () async {
      final read = Completer<int>(), write = Completer<void>();
      var loads = 0, written = false;
      LanternCancellationToken? token;
      final stream = coordinator.observe<int>(
        'query',
        initial: (_) => 0,
        load: (t) {
          token = t;
          loads++;
          return read.future;
        },
        markLost: (v, _) => v,
        interval: const Duration(seconds: 1),
      );
      final one = stream.listen((_) {}), two = stream.listen((_) {});
      runtime.enqueue('admitted', OfflineSessionLane.send, (work) async {
        await write.future;
        work.checkCurrent();
        written = true;
      });
      await flush();
      await clock.advance(const Duration(milliseconds: 50));
      expect(loads, 1);
      expect(runtime.jobCount, 2);
      await one.cancel();
      expect(token!.isCanceled, false);
      var canceled = false;
      final last = two.cancel().then((_) => canceled = true);
      await flush();
      expect(canceled, false);
      expect(token!.isCanceled, true);
      read.complete(1);
      await last;
      expect(runtime.jobCount, 1);
      write.complete();
      await flush();
      expect(written, true);
      expect(runtime.jobCount, 0);
    },
  );

  test(
    'S2 slow read plus 100 invalidations retains latest local pending and one followup',
    () async {
      var pending = 0, loads = 0, canceled = 0;
      final slow = Completer<int>();
      final values = <int>[];
      final sub = coordinator
          .observe<int>(
            'hot',
            initial: (_) => -1,
            captureLocal: () async => pending,
            load: (token) {
              token.listen((_) => canceled++);
              loads++;
              return loads == 1 ? slow.future : Future.value(200);
            },
            markLost: (v, _) => v,
          )
          .listen(values.add);
      await flush();
      await clock.advance(const Duration(milliseconds: 50));
      expect(loads, 1);
      for (var i = 1; i <= 100; i++) {
        pending = i;
        coordinator.invalidate();
      }
      await flush();
      expect(values.last, 100);
      expect(canceled, 0);
      expect(runtime.jobCount, 1);
      expect(runtime.inFlightCount, 1);
      slow.complete(7);
      await flush();
      expect(values, isNot(contains(7)));
      expect(canceled, 0);
      await clock.advance(const Duration(milliseconds: 250));
      expect(loads, 2);
      expect(values.last, 200);
      expect(runtime.jobCount, 1);
      await sub.cancel();
      expect(runtime.jobCount, 0);
    },
  );

  test(
    'S2 old denial after work rotation cannot pause the new watch',
    () async {
      final old = Completer<int>();
      final values = <int>[];
      var loads = 0;
      final sub = coordinator
          .observe<int>(
            'credential',
            initial: (_) => 0,
            load: (_) {
              loads++;
              return loads == 1 ? old.future : Future.value(2);
            },
            markLost: (v, _) => v,
            pauseAfter: (v) => v == -1,
            interval: const Duration(seconds: 1),
          )
          .listen(values.add);
      await flush();
      await clock.advance(const Duration(milliseconds: 50));
      final joined = runtime.pauseNetwork();
      coordinator.invalidate();
      old.complete(-1);
      await joined;
      runtime.advanceCredentialGeneration();
      coordinator.resume();
      await runtime.resumeNetwork();
      await clock.advance(const Duration(milliseconds: 50));
      expect(values, isNot(contains(-1)));
      expect(values, contains(2));
      final before = loads;
      await clock.advance(const Duration(seconds: 1));
      expect(loads, before + 1);
      await sub.cancel();
    },
  );

  test(
    'S3 retained slot caps reject atomically; reserved ordinary read and one timer',
    () async {
      await coordinator.close();
      await runtime.close();
      runtime = OfflineSessionRuntime(
        owner: TestOwner(),
        budget: OfflineSessionBudget(maxReads: 2, maxTraversals: 1),
        clock: clock.call,
        schedule: clock.schedule,
        fence: () {},
        onError: (e, _) => errors.add(e),
        maxReadJobs: 2,
        maxTraversalJobs: 2,
      );
      coordinator = LanternObservationCoordinator(
        maxQueries: 5,
        scheduler: runtime.observationScheduler,
      );
      final held = Completer<int>();
      var ordinary = 0, traversals = 0;
      Stream<int> query(String key, {bool traversal = false}) =>
          coordinator.observe(
            key,
            initial: (_) => 0,
            traversal: traversal,
            load: (_) {
              if (traversal) {
                traversals++;
                return held.future;
              }
              ordinary++;
              return Future.value(1);
            },
            markLost: (v, _) => v,
            interval: const Duration(seconds: 1),
          );
      final subs = [
        query('t1', traversal: true).listen((_) {}),
        query('t2', traversal: true).listen((_) {}),
        query('r1').listen((_) {}),
        query('r2').listen((_) {}),
      ];
      final rejected = <Object>[];
      final over = query('overflow').listen((_) {}, onError: rejected.add);
      await flush();
      expect(rejected.single, isA<LanternDataException>());
      expect(runtime.jobCount, 4);
      await clock.advance(const Duration(milliseconds: 50));
      expect(traversals, 1);
      expect(ordinary, 2);
      expect(clock.active, lessThanOrEqualTo(1));
      await subs.last.cancel();
      final replacement = query('replacement').listen((_) {});
      await flush();
      expect(runtime.jobCount, 4);
      held.complete(1);
      await flush();
      for (final s in subs) {
        await s.cancel();
      }
      await over.cancel();
      await replacement.cancel();
      expect(runtime.jobCount, 0);
    },
  );

  test(
    'S3 eight-event mailbox overflow emits loss and one revalidation',
    () async {
      var loads = 0, local = 0;
      final values = <(int, bool)>[];
      final sub = coordinator
          .observe<(int, bool)>(
            'mailbox',
            initial: (lost) => (-1, lost),
            captureLocal: () async => (local, false),
            load: (_) async => (++loads, false),
            markLost: (v, lost) => (v.$1, lost),
          )
          .listen(values.add);
      await flush();
      await clock.advance(const Duration(milliseconds: 50));
      sub.pause();
      for (var i = 0; i < 12; i++) {
        local++;
        coordinator.invalidate();
        await flush();
      }
      final before = loads;
      sub.resume();
      await flush();
      await clock.advance(const Duration(milliseconds: 50));
      expect(values.any((v) => v.$2), true);
      expect(loads, before + 1);
      await sub.cancel();
    },
  );

  test(
    'S4 auth pause keeps local maintenance, lifecycle suspend stops timer',
    () async {
      var calls = 0, maintenance = 0;
      final sub = coordinator
          .observe<int>(
            'watch',
            initial: (_) => 0,
            load: (_) async => ++calls,
            markLost: (v, _) => v,
            interval: const Duration(seconds: 1),
          )
          .listen((_) {});
      await flush();
      await clock.advance(const Duration(milliseconds: 50));
      await runtime.pauseNetwork();
      runtime.enqueue(
        'expiry',
        OfflineSessionLane.local,
        (_) async {
          maintenance++;
        },
        dueAt: clock.now.add(const Duration(seconds: 1)),
      );
      await clock.advance(const Duration(seconds: 1));
      expect(maintenance, 1);
      expect(calls, 1);
      expect(clock.active, 0);
      coordinator.resume();
      await runtime.resumeNetwork();
      await clock.advance(const Duration(milliseconds: 50));
      expect(calls, 2);
      await runtime.suspend();
      expect(clock.active, 0);
      await clock.advance(const Duration(seconds: 10));
      expect(calls, 2);
      runtime.resume();
      await flush();
      expect(calls, 3);
      await sub.cancel();
    },
  );

  test(
    'S1 Direct default coordinator remains independent of injection',
    () async {
      final direct = LanternObservationCoordinator();
      var calls = 0;
      final seen = <int>[];
      final sub = direct
          .observe<int>(
            'direct',
            initial: (_) => 0,
            load: (_) async => ++calls,
            markLost: (v, _) => v,
          )
          .listen(seen.add);
      await flush();
      expect(calls, 1);
      expect(seen, [0, 1]);
      await sub.cancel();
      await direct.close();
    },
  );
  test(
    'S4 hot and slow Traversal queries progress over 100 fixed turns',
    () async {
      final held = Completer<int>();
      var slow = 0, hot = 0, ordinary = 0, reconciled = 0;
      final subscriptions = <StreamSubscription<int>>[];
      for (final key in ['slow', 'hot', 'ordinary']) {
        subscriptions.add(
          coordinator
              .observe<int>(
                key,
                initial: (_) => 0,
                traversal: key != 'ordinary',
                load: (_) {
                  if (key == 'slow') {
                    slow++;
                    if (slow == 1) return held.future;
                  } else if (key == 'hot') {
                    hot++;
                  } else {
                    ordinary++;
                  }
                  return Future.value(1);
                },
                markLost: (value, _) => value,
                interval: const Duration(seconds: 1),
              )
              .listen((_) {}),
        );
      }
      for (var turn = 0; turn < 100; turn++) {
        coordinator.invalidateKey('hot');
        if (turn == 10) {
          runtime.enqueue('receipt-progress', OfflineSessionLane.reconcile, (
            _,
          ) async {
            reconciled++;
          });
        }
        if (turn == 25) {
          expect(ordinary, greaterThan(0));
          expect(reconciled, 1);
          held.complete(1);
        }
        await clock.advance(const Duration(milliseconds: 50));
        expect(clock.active, lessThanOrEqualTo(1));
        expect(runtime.jobCount, lessThanOrEqualTo(4));
      }
      expect(slow, greaterThan(1));
      expect(hot, greaterThan(1));
      expect(hot, lessThanOrEqualTo(101));
      for (final sub in subscriptions) {
        await sub.cancel();
      }
      expect(runtime.jobCount, 0);
      expect(clock.active, 0);
    },
  );
}
