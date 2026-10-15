import 'dart:async';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/src/errors.dart';
import 'package:lantern_client_offline/src/session_runtime.dart';
import 'package:test/test.dart';

import 'support/session_clock.dart';
import 'support/session_owner.dart';

void main() {
  late Clock clock;
  late TestOwner owner;
  late OfflineSessionBudget budget;
  late OfflineSessionRuntime runtime;
  late List<Object> errors;
  setUp(() {
    clock = Clock();
    owner = TestOwner();
    budget = OfflineSessionBudget();
    errors = [];
    runtime = OfflineSessionRuntime(
      owner: owner,
      budget: budget,
      clock: clock.call,
      schedule: clock.schedule,
      fence: () {},
      onError: (error, _) => errors.add(error),
    );
  });
  tearDown(() async {
    await runtime.close();
    expect(runtime.jobCount, 0);
    expect(runtime.inFlightCount, 0);
    expect(runtime.hasWakeup, isFalse);
    expect(clock.active, 0);
  });

  test(
    'hot invalidation reaches maximum wait without restarting work',
    () async {
      var runs = 0;
      Future<void> run(OfflineSessionWork work) async {
        work.checkCurrent();
        runs++;
      }

      runtime.enqueue('hot', OfflineSessionLane.read, run);
      for (var i = 0; i < 6; i++) {
        await clock.advance(const Duration(milliseconds: 40));
        runtime.enqueue('hot', OfflineSessionLane.read, run);
      }
      expect(runs, 0);
      expect(runtime.jobCount, 1);
      await clock.advance(const Duration(milliseconds: 10));
      expect(runs, 1);
      expect(clock.callbacks, 1);
      expect(errors, isEmpty);
    },
  );

  test(
    'absolute deadlines survive rollback and hints cannot postpone them',
    () async {
      var runs = 0;
      Future<void> run(OfflineSessionWork work) async => runs++;
      final due = clock.now.add(const Duration(seconds: 1));
      runtime.enqueue('retry', OfflineSessionLane.send, run, dueAt: due);
      await clock.advance(const Duration(milliseconds: 900));
      // A local invalidation may accelerate an attempt, but the queued absolute
      // deadline never moves later or resets durable input time/TTL.
      runtime.enqueue(
        'retry',
        OfflineSessionLane.send,
        run,
        dueAt: due.add(const Duration(seconds: 2)),
      );
      await clock.advance(const Duration(milliseconds: 100));
      expect(runs, 1);
      clock.now = clock.now.subtract(const Duration(days: 1));
      runtime.enqueue('past', OfflineSessionLane.send, run, dueAt: due);
      await clock.advance(Duration.zero);
      expect(runs, 2);
      expect(clock.callbacks, 2);
    },
  );

  test('one dirty slot behind slow work; no cancellation storm', () async {
    final held = Completer<void>();
    var runs = 0;
    LanternCancellationToken? token;
    Future<void> run(OfflineSessionWork work) async {
      runs++;
      token = work.cancellation;
      if (runs == 1) await held.future;
      work.checkCurrent();
    }

    runtime.enqueue('slow', OfflineSessionLane.traversal, run);
    await clock.advance(const Duration(milliseconds: 50));
    for (var i = 0; i < 100; i++) {
      runtime.enqueue('slow', OfflineSessionLane.traversal, run);
    }
    await clock.advance(const Duration(seconds: 1));
    expect(runs, 1);
    expect(runtime.jobCount, 1);
    expect(runtime.inFlightCount, 1);
    expect(runtime.deferred, isTrue);
    expect(token!.isCanceled, isFalse);
    held.complete();
    await flush();
    expect(runs, 2);
    expect(runtime.jobCount, 0);
    expect(errors, isEmpty);
  });

  test(
    'blocked identity and slow traversal leave independent lanes progressing',
    () async {
      final held = Completer<void>();
      final started = <String>[];
      for (final (key, lane) in [
        ('blocked', OfflineSessionLane.reconcile),
        ('slow', OfflineSessionLane.traversal),
        ('send', OfflineSessionLane.send),
        ('read', OfflineSessionLane.read),
        ('other-read', OfflineSessionLane.read),
      ]) {
        runtime.enqueue(key, lane, (work) async {
          started.add(key);
          if (key == 'blocked' || key == 'slow') await held.future;
          work.checkCurrent();
        });
      }
      await clock.advance(const Duration(milliseconds: 50));
      expect(started.toSet(), {
        'blocked',
        'slow',
        'send',
        'read',
        'other-read',
      });
      expect(runtime.inFlightCount, 2);
      held.complete();
      await flush();
      expect(runtime.jobCount, 0);
      expect(errors, isEmpty);
    },
  );

  test(
    'weighted round robin progresses every lane across 125 fixed jobs',
    () async {
      final order = <OfflineSessionLane>[];
      for (var round = 0; round < 25; round++) {
        for (final lane in OfflineSessionLane.values.where(
          (lane) => lane != OfflineSessionLane.local,
        )) {
          runtime.enqueue('$round:${lane.name}', lane, (work) async {
            work.checkCurrent();
            order.add(lane);
          });
        }
        await clock.advance(const Duration(milliseconds: 50));
      }
      expect(order.length, 125);
      for (final lane in OfflineSessionLane.values.where(
        (lane) => lane != OfflineSessionLane.local,
      )) {
        expect(order.where((l) => l == lane).length, 25);
      }
      expect(errors, isEmpty);
    },
  );

  test(
    'global permits and fair waiting owners do not spin zero timers',
    () async {
      await runtime.close();
      budget = OfflineSessionBudget(maxSends: 1);
      final otherClock = Clock();
      final other = OfflineSessionRuntime(
        owner: TestOwner(),
        budget: budget,
        clock: otherClock.call,
        schedule: otherClock.schedule,
        fence: () {},
        onError: (e, _) => errors.add(e),
      );
      addTearDown(other.close);
      runtime = OfflineSessionRuntime(
        owner: owner,
        budget: budget,
        clock: clock.call,
        schedule: clock.schedule,
        fence: () {},
        onError: (e, _) => errors.add(e),
      );
      final held = Completer<void>();
      final order = <String>[];
      runtime.enqueue('first', OfflineSessionLane.send, (_) async {
        order.add('first');
        await held.future;
      });
      await clock.advance(const Duration(milliseconds: 50));
      runtime.enqueue(
        'hot',
        OfflineSessionLane.send,
        (_) async => order.add('hot'),
      );
      other.enqueue(
        'other',
        OfflineSessionLane.send,
        (_) async => order.add('other'),
      );
      await otherClock.advance(const Duration(milliseconds: 50));
      await clock.advance(const Duration(milliseconds: 50));
      expect(order, ['first']);
      expect(clock.callbacks, 2);
      expect(otherClock.callbacks, 1);
      expect(runtime.hasWakeup, isFalse);
      expect(other.hasWakeup, isFalse);
      held.complete();
      await flush();
      expect(order, ['first', 'other', 'hot']);
      expect(errors, isEmpty);
    },
  );

  test(
    'global and per-scope slot caps reject atomically preserving work',
    () async {
      await runtime.close();
      budget = OfflineSessionBudget(maxReadJobs: 2);
      runtime = OfflineSessionRuntime(
        owner: owner,
        budget: budget,
        clock: clock.call,
        schedule: clock.schedule,
        fence: () {},
        onError: (e, _) => errors.add(e),
        maxReadJobs: 2,
      );
      var runs = 0;
      Future<void> run(OfflineSessionWork _) async => runs++;
      runtime.enqueue('a', OfflineSessionLane.read, run);
      runtime.enqueue('b', OfflineSessionLane.read, run);
      expect(
        () => runtime.enqueue('c', OfflineSessionLane.read, run),
        throwsA(isA<LanternDataException>()),
      );
      expect(
        () => runtime.enqueue('a', OfflineSessionLane.send, run),
        throwsA(isA<LanternDataException>()),
      );
      expect(runtime.jobCount, 2);
      await clock.advance(const Duration(milliseconds: 50));
      expect(runs, 2);
      runtime.enqueue('c', OfflineSessionLane.read, run);
      await clock.advance(const Duration(milliseconds: 50));
      expect(runs, 3);
      expect(errors, isEmpty);
    },
  );

  test(
    'suspend cancels, joins and fences late publication; resume rotates work',
    () async {
      final held = Completer<void>();
      var runs = 0, published = 0, joined = false;
      LanternCancellationToken? token;
      runtime.enqueue('send', OfflineSessionLane.send, (work) async {
        runs++;
        token = work.cancellation;
        if (runs == 1) await held.future;
        work.checkCurrent();
        published++;
      });
      await clock.advance(const Duration(milliseconds: 50));
      final suspended = runtime.suspend().then((_) => joined = true);
      expect(token!.isCanceled, isTrue);
      expect(() => runtime.resume(), throwsA(isA<LanternDataException>()));
      await clock.advance(const Duration(days: 1));
      expect(joined, isFalse);
      held.complete();
      await suspended;
      expect(published, 0);
      expect(runtime.jobCount, 1);
      runtime.resume();
      await flush();
      expect(runs, 2);
      expect(published, 1);
      expect(token!.isCanceled, isFalse);
      expect(errors, isEmpty);
    },
  );

  test(
    'last close waits for callbacks and rejects raced timer/publication',
    () async {
      final held = Completer<void>();
      var published = false, closed = false;
      runtime.enqueue('send', OfflineSessionLane.send, (work) async {
        await held.future;
        work.checkCurrent();
        published = true;
      });
      await clock.advance(const Duration(milliseconds: 50));
      runtime.enqueue(
        'read',
        OfflineSessionLane.read,
        (_) async => fail('closed timer ran'),
      );
      final raced = clock.alarms.last;
      final closing = runtime.close();
      expect(identical(closing, runtime.close()), isTrue);
      final joined = closing.then((_) => closed = true);
      raced.callback();
      await flush();
      expect(closed, isFalse);
      held.complete();
      await joined;
      expect(published, isFalse);
      expect(
        () => runtime.enqueue('x', OfflineSessionLane.read, (_) async {}),
        throwsA(isA<LanternDataException>()),
      );
      expect(errors, isEmpty);
    },
  );

  test(
    'unregister waits and preserves charged slot until canceled work joins',
    () async {
      await runtime.close();
      runtime = OfflineSessionRuntime(
        owner: owner,
        budget: budget,
        clock: clock.call,
        schedule: clock.schedule,
        fence: () {},
        onError: (e, _) => errors.add(e),
        maxJobs: 1,
      );
      final held = Completer<void>();
      var published = false;
      runtime.enqueue('read', OfflineSessionLane.read, (work) async {
        await held.future;
        work.checkCurrent();
        published = true;
      });
      await clock.advance(const Duration(milliseconds: 50));
      final removed = runtime.remove('read');
      expect(runtime.jobCount, 1);
      expect(
        () => runtime.enqueue('new', OfflineSessionLane.read, (_) async {}),
        throwsA(isA<LanternDataException>()),
      );
      held.complete();
      await removed;
      expect(published, isFalse);
      expect(runtime.jobCount, 0);
      runtime.enqueue('new', OfflineSessionLane.read, (_) async {});
      await clock.advance(const Duration(milliseconds: 50));
      expect(errors, isEmpty);
    },
  );

  test(
    'owner loss stops timer churn and fences an in-flight response',
    () async {
      final held = Completer<void>();
      var published = false;
      runtime.enqueue('flight', OfflineSessionLane.read, (work) async {
        await held.future;
        work.checkCurrent();
        published = true;
      });
      await clock.advance(const Duration(milliseconds: 50));
      runtime.enqueue(
        'queued',
        OfflineSessionLane.send,
        (_) async => fail('lost owner dispatched'),
      );
      owner.current = false;
      await clock.advance(const Duration(milliseconds: 50));
      expect(runtime.hasWakeup, isFalse);
      expect(errors.length, 1);
      held.complete();
      await flush();
      expect(published, isFalse);
      expect(errors.length, 1);
    },
  );

  test('failure reports once without implicit unsafe retry', () async {
    var runs = 0;
    runtime.enqueue('failure', OfflineSessionLane.send, (_) async {
      runs++;
      throw StateError('failure');
    });
    await clock.advance(const Duration(seconds: 1));
    await clock.advance(const Duration(days: 1));
    expect(runs, 1);
    expect(errors.single, isA<StateError>());
    expect(runtime.jobCount, 0);
  });

  test(
    'global slot cap spans owners and last close releases registrations',
    () async {
      await runtime.close();
      budget = OfflineSessionBudget(maxReadJobs: 1, maxRuntimes: 2);
      runtime = OfflineSessionRuntime(
        owner: owner,
        budget: budget,
        clock: clock.call,
        schedule: clock.schedule,
        fence: () {},
        onError: (e, _) => errors.add(e),
      );
      final otherClock = Clock();
      final other = OfflineSessionRuntime(
        owner: TestOwner(),
        budget: budget,
        clock: otherClock.call,
        schedule: otherClock.schedule,
        fence: () {},
        onError: (e, _) => errors.add(e),
      );
      addTearDown(other.close);
      expect(
        () => OfflineSessionRuntime(
          owner: TestOwner(),
          budget: budget,
          clock: clock.call,
          fence: () {},
          onError: (e, _) => errors.add(e),
        ),
        throwsA(isA<LanternDataException>()),
      );
      runtime.enqueue('one', OfflineSessionLane.read, (_) async {});
      expect(
        () => other.enqueue('two', OfflineSessionLane.read, (_) async {}),
        throwsA(isA<LanternDataException>()),
      );
      expect(other.jobCount, 0);
      await runtime.close();
      other.enqueue('two', OfflineSessionLane.read, (_) async {});
      await otherClock.advance(const Duration(milliseconds: 50));
      expect(other.jobCount, 0);
      expect(errors, isEmpty);
    },
  );

  test(
    'expensive work reserves an ordinary read under global saturation',
    () async {
      await runtime.close();
      budget = OfflineSessionBudget(maxReads: 2, maxTraversals: 1);
      runtime = OfflineSessionRuntime(
        owner: owner,
        budget: budget,
        clock: clock.call,
        schedule: clock.schedule,
        fence: () {},
        onError: (e, _) => errors.add(e),
      );
      final held = Completer<void>();
      var traversals = 0, reads = 0;
      for (var i = 0; i < 2; i++) {
        runtime.enqueue('traversal-$i', OfflineSessionLane.traversal, (
          work,
        ) async {
          traversals++;
          await held.future;
          work.checkCurrent();
        });
      }
      runtime.enqueue(
        'ordinary',
        OfflineSessionLane.read,
        (_) async => reads++,
      );
      await clock.advance(const Duration(milliseconds: 50));
      expect(traversals, 1);
      expect(reads, 1);
      expect(runtime.deferred, isTrue);
      expect(runtime.hasWakeup, isFalse);
      held.complete();
      await flush();
      expect(traversals, 2);
      expect(runtime.jobCount, 0);
      expect(errors, isEmpty);
    },
  );

  test(
    'R1 resume rechecks policy after joining and credentials fence retained work',
    () async {
      final held = Completer<void>();
      late OfflineSessionWork old;
      runtime.enqueue('old', OfflineSessionLane.send, (work) async {
        old = work;
        await held.future;
      }, retain: true);
      await clock.advance(const Duration(milliseconds: 50));
      expect(
        runtime.advanceCredentialGeneration,
        throwsA(isA<OfflineWorkInterruptedException>()),
      );
      final pausing = runtime.pauseNetwork();
      var allowed = true;
      final resuming = runtime.resumeNetwork(when: () => allowed);
      allowed = false;
      held.complete();
      await pausing;
      await resuming;
      var runs = 0;
      runtime.enqueue('new', OfflineSessionLane.read, (_) async => runs++);
      await clock.advance(const Duration(milliseconds: 50));
      expect(runs, 0);
      runtime.advanceCredentialGeneration();
      expect(old.checkCurrent, throwsA(isA<OfflineWorkInterruptedException>()));
      allowed = true;
      await runtime.resumeNetwork(when: () => allowed);
      await flush();
      expect(runs, 1);
      await runtime.remove('old');
    },
  );

  test(
    'R1 credential generation rejects a retained old work capability',
    () async {
      late OfflineSessionWork old;
      runtime.enqueue(
        'retained',
        OfflineSessionLane.read,
        (work) async => old = work,
        retain: true,
      );
      await clock.advance(const Duration(milliseconds: 50));
      old.checkCurrent();
      runtime.advanceCredentialGeneration();
      expect(old.checkCurrent, throwsA(isA<OfflineWorkInterruptedException>()));
      await runtime.remove('retained');
    },
  );

  test(
    'R1 network pause joins a one-shot already removing after cancellation',
    () async {
      final held = Completer<void>();
      final read = runtime.readOnce((work) async {
        await held.future;
        work.checkCurrent();
        return 1;
      });
      final stopped = expectLater(
        read,
        throwsA(isA<OfflineWorkInterruptedException>()),
      );
      await clock.advance(Duration.zero);
      var joined = false;
      final pause = runtime.pauseNetwork().then((_) => joined = true);
      await stopped;
      await flush();
      expect(joined, false);
      expect(runtime.networkInFlightCount, 1);
      held.complete();
      await pause;
      expect(runtime.networkInFlightCount, 0);
      expect(runtime.jobCount, 0);
    },
  );

  for (final inFlight in [false, true]) {
    test(
      'R3 ${inFlight ? 'in-flight' : 'queued'} read cancellation retires only its slot after join',
      () async {
        final token = LanternCancellationToken();
        final held = Completer<void>();
        LanternCancellationToken? workToken;
        final read = runtime.readOnce((work) async {
          workToken = work.cancellation;
          await held.future;
          work.checkCurrent();
          return 1;
        }, cancellation: token);
        final canceled = expectLater(
          read,
          throwsA(isA<LanternCanceledException>()),
        );
        if (inFlight) await clock.advance(Duration.zero);
        token.cancel();
        await canceled;
        expect(workToken?.isCanceled, inFlight ? true : isNull);
        if (inFlight) expect(runtime.networkInFlightCount, 1);
        held.complete();
        await flush();
        expect(runtime.jobCount, 0);
        expect(runtime.networkInFlightCount, 0);
        expect(errors, isEmpty);
        final next = runtime.readOnce((_) async => 2);
        await clock.advance(Duration.zero);
        expect(await next, 2);
      },
    );
  }
}
