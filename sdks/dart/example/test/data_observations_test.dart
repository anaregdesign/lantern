import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_example/app_data_session.dart';
import 'package:lantern_example/data_observations.dart';

import '../integration_test/support/installed_factory_fixture.dart';
import 'app_data_test_support.dart';

Future<void> drive(WidgetTester tester, AppHarness h) async {
  // Flush widget-zone admission before firing the injected runtime alarm. SQLite
  // also needs the real event loop; neither clock alone can drive both queues.
  await tester.pump(const Duration(milliseconds: 50));
  await tester.runAsync(() => h.clock.advance(const Duration(seconds: 1)));
  await tester.pump(const Duration(milliseconds: 50));
}

Future<void> disposeHarness(WidgetTester tester, AppHarness h) async {
  await tester.pumpWidget(const SizedBox.shrink());
  // Drain cancellations and ready widget work before joining real filesystem I/O.
  await tester.pump(Duration.zero);
  const reason = 'Account retirement must finish within the fixture budget';
  var done = false;
  Object? failure;
  StackTrace? trace;
  await tester.runAsync(() async {
    try {
      await h.close().timeout(
        const Duration(seconds: 5),
        onTimeout: () => fail(reason),
      );
      done = true;
    } catch (error, stack) {
      failure = error;
      trace = stack;
    }
  });
  if (failure != null) Error.throwWithStackTrace(failure!, trace!);
  expect(done, true, reason: reason);
}

void main() {
  testWidgets(
    'T18 raw observations close direct before a fresh memory widget',
    (tester) async {
      for (final profile in [AppDataProfile.direct, AppDataProfile.memory]) {
        final h = AppHarness(profile);
        await tester.runAsync(h.open);
        addTearDown(() => tester.runAsync(h.close));
        h.wire.vertices.addAll({
          'app:alice': {'key': 'app:alice', 'string': 'initial'},
          'app:bob': {'key': 'app:bob', 'string': 'endpoint'},
        });
        h.wire.edges['app:alice\u0000app:bob'] = {
          'tail': 'app:alice',
          'head': 'app:bob',
          'weight': 1,
        };
        final observations = InstalledFactoryObservations();
        final events = <LanternSnapshot<Vertex>>[];
        final watch = observations.listen(
          h.data.watchVertex('app:alice'),
          events.add,
        );
        final traversals = <LanternTraversalSnapshot>[];
        final traversal = observations.listen(
          h.data.watchIlluminate(
            'app:alice',
            traversal: const BfsOptions(step: 2, fanOut: 10),
            vertexPrefix: 'app:',
          ),
          traversals.add,
        );
        addTearDown(() => tester.runAsync(watch.cancel));
        addTearDown(() => tester.runAsync(traversal.cancel));
        await tester.pumpWidget(
          MaterialApp(
            home: DataObservationsScreen(
              key: ObjectKey(h.owner),
              session: h.owner,
              prefix: 'app:',
            ),
          ),
        );
        for (var turn = 0; turn < 20; turn++) {
          observations.check();
          await tester.pump(const Duration(milliseconds: 50));
          await tester.runAsync(
            () => h.clock.advance(const Duration(seconds: 1)),
          );
          await tester.pump(const Duration(milliseconds: 50));
          if (tester
                  .widget<Text>(find.byKey(const Key('live-vertex')))
                  .data!
                  .contains('initial') &&
              tester
                  .widget<Text>(find.byKey(const Key('live-edge')))
                  .data!
                  .contains('present: 1.0') &&
              tester
                  .widget<Text>(find.byKey(const Key('live-traversal')))
                  .data!
                  .contains('1 edges')) {
            break;
          }
        }
        expect(
          tester.widget<Text>(find.byKey(const Key('live-vertex'))).data,
          contains('initial'),
        );
        expect(
          tester.widget<Text>(find.byKey(const Key('live-edge'))).data,
          contains('present: 1.0'),
        );
        expect(
          tester.widget<Text>(find.byKey(const Key('live-traversal'))).data,
          contains('1 edges'),
        );
        await tester.pumpWidget(const SizedBox.shrink());
        await tester.pump(Duration.zero);
        await tester.runAsync(
          () => observations.retire(() async {
            final closing = h.owner.close();
            expect(identical(closing, h.owner.close()), isTrue);
            await closing.timeout(const Duration(seconds: 5));
          }),
        );
        if (profile == AppDataProfile.direct) {
          expect(observations.closedErrors, 2);
        }
        final eventCount = events.length,
            traversalCount = traversals.length,
            tokenReads = h.tokenReads,
            calls = h.wire.calls.length;
        await tester.runAsync(
          () => h.clock.advance(const Duration(seconds: 1)),
        );
        await tester.pump();
        observations.check();
        expect(events, hasLength(eventCount));
        expect(traversals, hasLength(traversalCount));
        expect(h.tokenReads, tokenReads);
        expect(h.wire.calls, hasLength(calls));
        expect(h.clock.alarms.where((alarm) => !alarm.canceled), isEmpty);
        expect(h.owner.isClosed, isTrue);
        expect(h.client.isClosed, isTrue);
        expect(h.closes, 1);
        expect(tester.takeException(), isNull);
        await tester.runAsync(() async {
          await watch.cancel();
          await traversal.cancel();
        });
      }
    },
  );

  for (final profile in AppDataProfile.values) {
    testWidgets(
      'I1/I7 same foreground widget ${profile.name} after transport recovery',
      (tester) async {
        final h = AppHarness(profile);
        await tester.runAsync(h.open);
        addTearDown(() => disposeHarness(tester, h));
        h.wire.available = false;
        await tester.pumpWidget(
          MaterialApp(
            home: DataObservationsScreen(session: h.owner, prefix: 'app:'),
          ),
        );
        await tester.enterText(find.byType(TextField), 'screen-value');
        await tester.tap(find.text('Submit locally'));
        for (var turn = 0; turn < 20 && h.wire.blocked == 0; turn++) {
          await drive(tester, h);
        }
        expect(h.wire.blocked, greaterThan(0));
        expect(find.text('Sync'), findsNothing);
        expect(find.text('Get'), findsNothing);
        h.wire.available = true;
        if (profile == AppDataProfile.direct) {
          // Failed direct calls have no replay queue. A new explicit user write is
          // needed; ordinary Put still returns the same Server-handling contract.
          await tester.tap(find.text('Put'));
        }
        for (var turn = 0; turn < 20; turn++) {
          await drive(tester, h);
          if (tester
              .widget<Text>(find.byKey(const Key('live-vertex')))
              .data!
              .contains('screen-value')) {
            break;
          }
        }
        expect(
          tester.widget<Text>(find.byKey(const Key('live-vertex'))).data,
          contains('screen-value'),
        );
        expect(tester.takeException(), isNull);
      },
    );
  }

  testWidgets(
    'I6 widget retirement joins real filesystem cleanup without advancing fake time',
    (tester) async {
      final h = AppHarness(AppDataProfile.direct);
      await tester.runAsync(h.open);
      final original = h.directory!;
      await tester.pumpWidget(
        MaterialApp(
          home: DataObservationsScreen(session: h.owner, prefix: 'app:'),
        ),
      );
      final beforeClose = tester.binding.clock.now();
      (bool, bool, int, DateTime)? atDeletion;
      var deleted = false;
      final directory = _RetirementDirectory(original, (recursive) async {
        atDeletion = (
          h.owner.isClosed,
          h.client.isClosed,
          h.closes,
          tester.binding.clock.now(),
        );
        await Future<void>.delayed(Duration.zero);
        final result = await original.delete(recursive: recursive);
        deleted = true;
        return result;
      });
      h.directory = directory;
      try {
        await disposeHarness(tester, h);
        expect(atDeletion, (true, true, 1, beforeClose));
        expect(deleted, true);
        expect(directory.deleting, isNotNull);
        expect(h.clock.alarms.where((a) => !a.canceled), isEmpty);
        await tester.runAsync(() async {
          expect(await original.exists(), false);
        });
        expect(tester.takeException(), isNull);
      } finally {
        await tester.runAsync(() async {
          if (await original.exists()) await original.delete(recursive: true);
        });
      }
    },
  );

  testWidgets(
    'I6 widget retirement preserves cleanup failure and original stack',
    (tester) async {
      final h = AppHarness(AppDataProfile.direct);
      await tester.runAsync(h.open);
      final original = h.directory!;
      final failure = FileSystemException(
        'fixture deletion failed',
        original.path,
      );
      final trace = StackTrace.fromString('fixture directory deletion origin');
      h.directory = _RetirementDirectory(original, (_) async {
        Error.throwWithStackTrace(failure, trace);
      });
      await tester.pumpWidget(
        MaterialApp(
          home: DataObservationsScreen(session: h.owner, prefix: 'app:'),
        ),
      );
      Object? caught;
      StackTrace? caughtTrace;
      try {
        try {
          await disposeHarness(tester, h);
        } catch (error, stack) {
          caught = error;
          caughtTrace = stack;
        }
        expect(caught, same(failure));
        expect(caughtTrace.toString(), trace.toString());
        expect(h.owner.isClosed, true);
        expect(h.closes, 1);
        expect(tester.takeException(), isNull);
      } finally {
        await tester.runAsync(() => original.delete(recursive: true));
      }
    },
  );

  testWidgets(
    'I6 widget retirement rejects cleanup stalled beyond the fixture budget',
    (tester) async {
      final h = AppHarness(AppDataProfile.direct);
      await tester.runAsync(h.open);
      final original = h.directory!;
      final deletion = Completer<FileSystemEntity>();
      final directory = _RetirementDirectory(original, (_) => deletion.future);
      h.directory = directory;
      await tester.pumpWidget(
        MaterialApp(
          home: DataObservationsScreen(session: h.owner, prefix: 'app:'),
        ),
      );
      final beforeClose = tester.binding.clock.now();
      Object? caught;
      try {
        try {
          await disposeHarness(tester, h);
        } catch (error) {
          caught = error;
        }
        expect(
          caught,
          isA<TestFailure>().having(
            (error) => error.message,
            'message',
            contains(
              'Account retirement must finish within the fixture budget',
            ),
          ),
        );
        expect(directory.deleting, isNotNull);
        expect(h.owner.isClosed, true);
        expect(h.closes, 1);
        expect(tester.binding.clock.now(), beforeClose);
        expect(tester.takeException(), isNull);
      } finally {
        await tester.runAsync(() async {
          deletion.complete(await original.delete(recursive: true));
        });
        await tester.pump();
        await directory.deleting;
      }
    },
  );
}

final class _RetirementDirectory implements Directory {
  _RetirementDirectory(this.original, this.deleteOperation);

  final Directory original;
  final Future<FileSystemEntity> Function(bool recursive) deleteOperation;
  Future<FileSystemEntity>? deleting;

  @override
  String get path => original.path;

  @override
  Future<FileSystemEntity> delete({bool recursive = false}) =>
      deleting = deleteOperation(recursive);

  @override
  Never noSuchMethod(Invocation invocation) =>
      throw UnsupportedError('Unexpected Directory operation');
}
