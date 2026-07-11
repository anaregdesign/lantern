import 'dart:convert';

import 'package:lantern_connect_transport_probe/probe.dart';

Future<void> main() async {
  const plaintextUrl = String.fromEnvironment('LANTERN_PROBE_PLAINTEXT_URL');
  const tlsUrl = String.fromEnvironment('LANTERN_PROBE_TLS_URL');
  const token = String.fromEnvironment('LANTERN_PROBE_TOKEN');
  const caPemBase64 = String.fromEnvironment('LANTERN_PROBE_CA_PEM_BASE64');
  const leafBase64 = String.fromEnvironment('LANTERN_PROBE_LEAF_BASE64');
  if ([
    plaintextUrl,
    tlsUrl,
    token,
    caPemBase64,
    leafBase64,
  ].any((value) => value.isEmpty)) {
    throw StateError('mobile probe configuration is incomplete');
  }

  _expectSuccess(await runProbe(Uri.parse(plaintextUrl)));
  final trustedTls = Uri.parse(tlsUrl);
  final wrongHostTls = trustedTls.replace(host: '127.0.0.1');
  final pins = <String>[
    utf8.decode(base64Decode(caPemBase64)),
    utf8.decode(base64Decode(leafBase64)),
  ];

  await _expectFailure(runProbe(wrongHostTls, token: token));
  await _expectFailure(runProbe(trustedTls, pinnedCertificatePems: pins));
  _expectSuccess(
    await runProbe(trustedTls, token: token, pinnedCertificatePems: pins),
  );

  // A host-side CountVerticesByPrefix call uses this second request as the
  // success signal. Reaching it proves the complete contract above passed.
  await runProbe(
    trustedTls,
    token: token,
    keyPrefix: 'probe/connect/ios-success/',
    pinnedCertificatePems: pins,
  );
}

Future<void> _expectFailure(Future<Object?> operation) async {
  try {
    await operation;
  } catch (_) {
    return;
  }
  throw StateError('operation unexpectedly succeeded');
}

void _expectSuccess(Map<String, Object> result) {
  if (result['transport'] != 'connect-http1' ||
      result['int64'] != '922337203685477580' ||
      result['headerCallback'] != true ||
      result['trailerCallback'] != true ||
      (result['streamRecordsBeforeCancel']! as int) < 1) {
    throw StateError('Connect-Dart mobile contract mismatch: $result');
  }
}
