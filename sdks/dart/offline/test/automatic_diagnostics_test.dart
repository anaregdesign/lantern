import 'dart:async';
import 'dart:convert';

import 'package:test/test.dart';

import '../tool/automatic_diagnostics.dart';

void main() {
  test(
    'deadline stays failed when a progressing ticket completes during join',
    () async {
      var now = 0, replies = 9;
      final deadline = Completer<void>(), ticket = Completer<void>();
      final events = <Map<String, dynamic>>[];
      Future<T> bound<T>(Future<T> future, Duration limit) =>
          limit.inSeconds == 30
          ? Future.any([
              future,
              deadline.future.then<T>(
                (_) => throw TimeoutException('deadline'),
              ),
            ])
          : future;
      final diagnostic = AutomaticDiagnostics(
        write: (v) =>
            events.add(jsonDecode(jsonEncode(v)) as Map<String, dynamic>),
        micros: () => now,
        bound: bound,
      )..passive = () => {'repliesReturned': replies};
      final waiting = expectLater(
        diagnostic.waitTicket(ticket.future),
        throwsA(isA<TimeoutException>()),
      );
      now = 30000000;
      deadline.complete();
      await waiting;
      expect(diagnostic.timedOut, isTrue);
      expect(events.single['ticketResolved'], isFalse);
      expect((events.single['passive'] as Map)['repliesReturned'], 9);
      await diagnostic.inspectStopped(
        suspend: () async {
          replies = 10;
          now++;
          ticket.complete();
          await Future<void>.value();
        },
        snapshot: () async => {'confirmed': 10, 'outbox': 0},
      );
      expect(diagnostic.ticketResolved, isTrue);
      expect(diagnostic.timedOut, isTrue);
      expect(events.first['ticketResolved'], isFalse);
      expect(events.map((e) => e['event']), ['deadline', 'joined', 'postJoin']);
      expect(events.every((e) => e['qualifies'] == false), isTrue);
    },
  );

  test(
    'durable terminal commit before cutoff remains distinct from pending waiter',
    () async {
      final events = <Map<String, Object?>>[];
      Future<T> bound<T>(Future<T> future, Duration limit) async =>
          throw TimeoutException('deadline');
      final diagnostic = AutomaticDiagnostics(
        write: events.add,
        micros: () => 30000000,
        bound: bound,
      );
      diagnostic.database = () => {'terminalCommitMicros': 29000000};
      diagnostic.passive = () => {'repliesReturned': 100};
      await expectLater(
        diagnostic.waitTicket(Completer<void>().future),
        throwsA(isA<TimeoutException>()),
      );
      expect(events.single['ticketResolved'], isFalse);
      expect(events.single['database'], {'terminalCommitMicros': 29000000});
      expect(events.single['passive'], {'repliesReturned': 100});
      expect(events.single['timeoutLatched'], isTrue);
      expect(events.single['qualifies'], isFalse);
    },
  );

  test(
    'unconfirmed join retains deadline evidence and never claims snapshot or owner release',
    () async {
      final events = <Map<String, Object?>>[];
      var snapshotCalls = 0;
      Future<T> bound<T>(Future<T> future, Duration limit) async =>
          throw TimeoutException('bounded observation');
      final diagnostic = AutomaticDiagnostics(
        write: events.add,
        micros: () => 30000000,
        bound: bound,
      );
      await expectLater(
        diagnostic.waitTicket(Completer<void>().future),
        throwsA(isA<TimeoutException>()),
      );
      final joined = await diagnostic.inspectStopped(
        suspend: () => Completer<void>().future,
        snapshot: () async {
          snapshotCalls++;
          return {};
        },
      );
      expect(joined, isFalse);
      expect(snapshotCalls, 0);
      expect(diagnostic.cleanupConfirmed, isFalse);
      expect(diagnostic.timedOut, isTrue);
      expect(events.map((e) => e['event']), ['deadline', 'joinUnconfirmed']);
    },
  );
}
