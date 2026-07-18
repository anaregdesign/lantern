import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('native mobile rejects an untrusted TLS certificate', (
    tester,
  ) async {
    const endpointValue = String.fromEnvironment('LANTERN_ENDPOINT');
    expect(endpointValue, isNotEmpty, reason: 'pass LANTERN_ENDPOINT');
    final client = LanternClient.connect(
      Uri.parse(endpointValue),
      retryPolicy: const RetryPolicy(maxAttempts: 1),
    );
    addTearDown(client.close);

    try {
      await client.ping();
      fail('untrusted TLS certificate unexpectedly passed verification');
    } on LanternUnavailableException catch (error) {
      final cause = '${error.cause}';
      // ignore: avoid_print
      print('UNTRUSTED_TLS_REJECTED ${error.cause.runtimeType}: $cause');
      expect(cause, contains('CERTIFICATE_VERIFY_FAILED'));
    }
  });
}
