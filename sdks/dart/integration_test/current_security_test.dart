import 'dart:io' as io;
import 'package:lantern_client/lantern_client.dart';
import 'package:test/test.dart';

void main() {
  final endpoint = io.Platform.environment['LANTERN_CURRENT_WIRE_URL'];
  test(
    'current native public binding over authenticated TLS',
    () async {
      final context = io.SecurityContext(withTrustedRoots: false)
        ..setTrustedCertificates(
          io.Platform.environment['LANTERN_CURRENT_WIRE_CA']!,
        );
      final client = LanternClient.connect(
        Uri.parse(endpoint!),
        httpClientFactory: () => io.HttpClient(context: context),
        tokenProvider: () =>
            io.Platform.environment['LANTERN_CURRENT_WIRE_CREDENTIAL'],
        defaultTimeout: const Duration(seconds: 5),
      );
      addTearDown(client.close);
      final first = await client.getCurrentAuthorityBinding();
      expect(first, startsWith('current-v2:'));
      expect(first, contains('/cut-v1:'));
      expect(await client.getCurrentAuthorityBinding(), first);
      await client.putVertex(
        const VertexInput(
          key: 'orders:dart-current',
          value: StringValue('current'),
        ),
      );
      final found = await client.getVertex('orders:dart-current');
      expect((found.value as StringValue).value, 'current');
    },
    skip: endpoint == null
        ? 'Run through the native public SDK4 root gate.'
        : false,
  );
}
