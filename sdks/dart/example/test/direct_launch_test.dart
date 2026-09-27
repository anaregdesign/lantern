import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

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

  directLaunchIntegrationTest(description, () async {
    expect(binding.inTest, isTrue);
    addTearDown(() {
      expect(binding.results[description], 'success');
      expect(binding.inTest, isTrue);
    });
  });
}
