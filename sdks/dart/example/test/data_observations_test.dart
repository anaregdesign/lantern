import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_example/app_data_session.dart';
import 'package:lantern_example/data_observations.dart';

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
