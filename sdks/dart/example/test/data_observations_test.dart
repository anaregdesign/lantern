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
  var done = false;
  Object? failure;
  final closing = h.close().then<void>(
    (_) => done = true,
    onError: (Object error) {
      failure = error;
      done = true;
    },
  );
  for (var turn = 0; turn < 100 && !done; turn++) {
    await tester.pump(const Duration(milliseconds: 50));
    await tester.runAsync(() => Future<void>.delayed(Duration.zero));
  }
  expect(
    done,
    true,
    reason: 'Account retirement must finish within the fixture budget',
  );
  await closing;
  if (failure != null) throw failure!;
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
}
