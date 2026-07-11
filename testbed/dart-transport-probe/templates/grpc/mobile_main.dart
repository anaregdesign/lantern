import 'dart:convert';
import 'dart:io';

import 'package:lantern_grpc_transport_probe/probe.dart';

Future<void> main() async {
  const plaintextUrl = String.fromEnvironment('LANTERN_PROBE_PLAINTEXT_URL');
  const tlsUrl = String.fromEnvironment('LANTERN_PROBE_TLS_URL');
  const token = String.fromEnvironment('LANTERN_PROBE_TOKEN');
  const caBase64 = String.fromEnvironment('LANTERN_PROBE_CA_BASE64');
  if ([plaintextUrl, tlsUrl, token, caBase64].any((value) => value.isEmpty)) {
    throw StateError('mobile probe configuration is incomplete');
  }

  _expectSuccess(await runProbe(Uri.parse(plaintextUrl)));
  final caFile = File('${Directory.systemTemp.path}/lantern-probe-ca.der');
  await caFile.writeAsBytes(base64Decode(caBase64));

  await _expectFailure(runProbe(Uri.parse(tlsUrl), token: token));
  await _expectFailure(runProbe(Uri.parse(tlsUrl), caPath: caFile.path));
  _expectSuccess(
    await runProbe(Uri.parse(tlsUrl), token: token, caPath: caFile.path),
  );

  // A host-side CountVerticesByPrefix call uses this second request as the
  // success signal. Reaching it proves the complete contract above passed.
  await runProbe(
    Uri.parse(tlsUrl),
    token: token,
    caPath: caFile.path,
    keyPrefix: 'probe/grpc/ios-success/',
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
  if (result['transport'] != 'grpc-http2' ||
      result['int64'] != '922337203685477580' ||
      (result['streamRecordsBeforeCancel']! as int) < 1) {
    throw StateError('gRPC-Dart mobile contract mismatch: $result');
  }
}
