import 'dart:io' as io;

import 'package:lantern_client/lantern_client.dart';
import 'package:test/test.dart';

void main() {
  test('health ping reaches a real Lantern listener when configured', () async {
    final configured =
        io.Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
    if (configured == null || configured.isEmpty) {
      markTestSkipped('LANTERN_DART_REAL_WIRE_ENDPOINT is not configured');
      return;
    }
    final endpoint = Uri.parse(configured);
    final client = LanternClient.connect(
      endpoint,
      allowInsecure: endpoint.scheme == 'http',
      defaultTimeout: const Duration(seconds: 5),
    );
    addTearDown(client.close);
    await client.ping();
  });
}
