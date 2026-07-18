import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('iOS Local Network denial fails closed', (tester) async {
    const endpointValue = String.fromEnvironment('LANTERN_ENDPOINT');
    expect(endpointValue, isNotEmpty, reason: 'pass LANTERN_ENDPOINT');
    final client = LanternClient.connect(
      Uri.parse(endpointValue),
      allowInsecure: true,
      retryPolicy: const RetryPolicy(maxAttempts: 1),
    );
    addTearDown(client.close);

    try {
      await client.ping();
      fail('Local Network denial unexpectedly allowed the request');
    } on LanternUnavailableException catch (error) {
      final cause = '${error.cause}';
      // ignore: avoid_print
      print('LOCAL_NETWORK_DENIED ${error.cause.runtimeType}: $cause');
      expect(cause, contains('No route to host'));
    }
  });
}
