import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_example/main.dart';
import 'package:lantern_example/offline_session.dart';

void main() {
  test('database identity is stable and separated by endpoint and account', () {
    final endpoint = Uri.parse('https://lantern.example.com');
    final filename = offlineDatabaseFileName(endpoint, 'tenant/user');
    expect(filename, offlineDatabaseFileName(endpoint, 'tenant/user'));
    expect(filename, matches(RegExp(r'^lantern-offline-[0-9a-f]{64}\.db$')));
    expect(filename, isNot(contains('tenant')));
    expect(filename, isNot(offlineDatabaseFileName(endpoint, 'tenant/other')));
    expect(
      filename,
      isNot(
        offlineDatabaseFileName(
          Uri.parse('https://other.example.com'),
          'tenant/user',
        ),
      ),
    );
    expect(() => offlineDatabaseFileName(endpoint, ' '), throwsArgumentError);
  });

  test('authenticated configuration requires explicit non-secret scope', () {
    final endpoint = Uri.parse('https://lantern.example.com');
    final tokenEndpoint = Uri.parse('https://bff.example.com/token');
    expect(
      () => DemoConfiguration(
        endpoint: endpoint,
        tokenEndpoint: tokenEndpoint,
        allowInsecure: false,
      ).offlinePartitionId,
      throwsStateError,
    );
    expect(
      DemoConfiguration(
        endpoint: endpoint,
        tokenEndpoint: tokenEndpoint,
        allowInsecure: false,
        offlineScope: 'tenant/account',
      ).offlinePartitionId,
      'tenant/account',
    );
    expect(
      DemoConfiguration(
        endpoint: endpoint,
        tokenEndpoint: null,
        allowInsecure: false,
      ).offlinePartitionId,
      'anonymous',
    );
  });

  test('foreground CDC requires an explicit pinned-responder opt-in', () {
    final endpoint = Uri.parse('https://lantern.example.com');
    expect(
      DemoConfiguration(
        endpoint: endpoint,
        tokenEndpoint: null,
        allowInsecure: false,
      ).offlineCdcPinnedResponder,
      isFalse,
    );
    expect(
      DemoConfiguration(
        endpoint: endpoint,
        tokenEndpoint: null,
        allowInsecure: false,
        offlineCdcPinnedResponder: true,
      ).offlineCdcPinnedResponder,
      isTrue,
    );
  });

  testWidgets(
    'missing account scope fails before storage and disposes cleanly',
    (tester) async {
      await tester.pumpWidget(
        LanternExampleApp(
          configuration: DemoConfiguration(
            endpoint: Uri.parse('https://lantern.example.com'),
            tokenEndpoint: Uri.parse('https://bff.example.com/token'),
            allowInsecure: false,
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('offline-storage-error')), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
    },
  );
}
