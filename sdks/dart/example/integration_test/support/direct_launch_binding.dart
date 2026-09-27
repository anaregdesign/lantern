import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

/// Runs an integration test without WidgetTester semantics-handle baselines
/// while still reporting its result to the native and Flutter Driver bindings.
void directLaunchIntegrationTest(
  String description,
  Future<void> Function() body,
) {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  test(description, () async {
    // Registered before the body so its cleanup and result marker run first.
    addTearDown(binding.postTest);
    await binding.runTest(body, () {}, description: description);
  });
}
