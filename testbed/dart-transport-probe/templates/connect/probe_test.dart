import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_connect_transport_probe/probe.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('Connect-Dart mobile transport contract', (_) async {
    const plaintextUrl = String.fromEnvironment('LANTERN_PROBE_PLAINTEXT_URL');
    const tlsUrl = String.fromEnvironment('LANTERN_PROBE_TLS_URL');
    const token = String.fromEnvironment('LANTERN_PROBE_TOKEN');
    const caBase64 = String.fromEnvironment('LANTERN_PROBE_CA_BASE64');
    expect(plaintextUrl, isNotEmpty);
    expect(tlsUrl, isNotEmpty);
    expect(token, isNotEmpty);
    expect(caBase64, isNotEmpty);

    final plaintext = await runProbe(Uri.parse(plaintextUrl));
    _expectSuccess(plaintext);

    // SecurityContext accepts a single DER-encoded certificate on iOS.
    final caFile = File('${Directory.systemTemp.path}/lantern-probe-ca.der');
    await caFile.writeAsBytes(base64Decode(caBase64));

    await expectLater(
      runProbe(Uri.parse(tlsUrl), token: token),
      throwsA(anything),
      reason: 'a self-signed server must fail closed before CA injection',
    );
    await expectLater(
      runProbe(Uri.parse(tlsUrl), caPath: caFile.path),
      throwsA(anything),
      reason: 'the TLS endpoint must reject missing auth metadata',
    );

    final trusted = await runProbe(
      Uri.parse(tlsUrl),
      token: token,
      caPath: caFile.path,
    );
    _expectSuccess(trusted);
  });
}

void _expectSuccess(Map<String, Object> result) {
  expect(result['transport'], 'connect-http1');
  expect(result['int64'], '922337203685477580');
  expect(result['headerCallback'], isTrue);
  expect(result['trailerCallback'], isTrue);
  expect(result['streamRecordsBeforeCancel'], greaterThanOrEqualTo(1));
}
