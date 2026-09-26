import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'support/receipt_attestation.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  test('nonqualifying installed receipt binary probe', () async {
    final output = File(
      '${Directory.systemTemp.path}/lantern-receipt-binary-probe.json',
    );
    if (await output.exists()) await output.delete();
    final binary = await readInstalledReceiptBinary();
    expect(binary.platform, Platform.isAndroid ? 'android' : 'ios');
    expect(binary.sha256, matches(RegExp(r'^[0-9a-f]{64}$')));
    final fields = {
      'kind': 'nonqualifying_installed_binary_probe',
      'platform': binary.platform,
      'packageId': binary.packageId,
      'sha256': binary.sha256,
      'capturedAt': DateTime.now().toUtc().toIso8601String(),
    };
    final temporary = File('${output.path}.tmp');
    await temporary.writeAsString(jsonEncode(fields), flush: true);
    await temporary.rename(output.path);
    // A probe's hash is never evidence for a future receipt test target.
    // ignore: avoid_print
    print(
      'RECEIPT_BINARY_PROBE platform=${binary.platform} sha256=${binary.sha256}',
    );
  });
}
