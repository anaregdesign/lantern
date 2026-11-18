import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import '../integration_test/mobile_smoke_test.dart' as smoke;
import '../integration_test/support/direct_launch_binding.dart';

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  const description = 'direct launch reports its result to integration_test';
  const channel = MethodChannel('plugins.flutter.io/integration_test');
  TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
      .setMockMethodCallHandler(channel, (call) async {
        expect(call.method, 'allTestsFinished');
        final report = call.arguments as Map<Object?, Object?>;
        final results = report['results'] as Map<Object?, Object?>;
        expect(results[description], 'success');
        return null;
      });

  test('only the direct-launch simulator supplies a mobile smoke timeout', () {
    final timeouts = <Timeout?>[];
    void capture(
      String name,
      Future<void> Function() body, {
      Timeout? timeout,
    }) {
      expect(name, 'native mobile real-wire smoke');
      timeouts.add(timeout);
    }

    smoke.registerMobileSmoke(capture, iosDirectLaunch: true);
    smoke.registerMobileSmoke(capture, iosDirectLaunch: false);
    expect(timeouts, [const Timeout(Duration(minutes: 3)), null]);
  });

  directLaunchIntegrationTest(description, () async {
    expect(binding.inTest, isTrue);
    addTearDown(() {
      expect(binding.results[description], 'success');
      expect(binding.inTest, isTrue);
    });
  });

  directLaunchIntegrationTest(
    'direct launch pumps and interacts with the existing widget binding',
    () async {
      var taps = 0;
      runApp(
        MaterialApp(
          home: Scaffold(
            body: FilledButton(
              onPressed: () => taps++,
              child: const Text('Submit locally'),
            ),
          ),
        ),
      );
      await binding.pump();
      await DirectLaunchWidgetController(
        binding,
      ).tap(find.text('Submit locally'));
      await binding.pump(const Duration(milliseconds: 150));
      expect(taps, 1);
      runApp(const SizedBox.shrink());
      await binding.pump();
    },
    timeout: const Timeout(Duration(seconds: 10)),
  );
}
