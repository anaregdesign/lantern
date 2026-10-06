import 'dart:convert';

import 'package:lantern_grpc_transport_probe/probe.dart';

import 'probe_diagnostics.dart';

Future<void> main() async {
  final diagnostics = ProbeDiagnostics(
    transport: 'grpc',
    errorCode: probeErrorCode,
  );
  await diagnostics.run(() => _runScenarios(diagnostics));
}

Future<void> _runScenarios(ProbeDiagnostics diagnostics) async {
  const plaintextUrl = String.fromEnvironment('LANTERN_PROBE_PLAINTEXT_URL');
  const tlsUrl = String.fromEnvironment('LANTERN_PROBE_TLS_URL');
  const token = String.fromEnvironment('LANTERN_PROBE_TOKEN');
  const caPemBase64 = String.fromEnvironment('LANTERN_PROBE_CA_PEM_BASE64');
  const caBase64 = String.fromEnvironment('LANTERN_PROBE_CA_BASE64');
  const leafBase64 = String.fromEnvironment('LANTERN_PROBE_LEAF_BASE64');
  if ([
    plaintextUrl,
    tlsUrl,
    token,
    caPemBase64,
    caBase64,
    leafBase64,
  ].any((value) => value.isEmpty)) {
    throw StateError('mobile probe configuration is incomplete');
  }

  await diagnostics.scenario('plaintext', () async {
    _expectSuccess(
      await runProbe(Uri.parse(plaintextUrl), onRpc: diagnostics.rpc),
    );
  });
  final trustedTls = Uri.parse(tlsUrl);
  final wrongHostTls = trustedTls.replace(host: '127.0.0.1');
  final pins = <String>[
    utf8.decode(base64Decode(caPemBase64)),
    utf8.decode(base64Decode(leafBase64)),
  ];

  await diagnostics.scenario('wrong_host', () async {
    await runProbe(
      wrongHostTls,
      token: token,
      trustedCertificateBytes: base64Decode(caBase64),
      onRpc: diagnostics.rpc,
    );
  });
  await diagnostics.scenario('missing_auth', () async {
    await runProbe(
      trustedTls,
      pinnedCertificatePems: pins,
      onRpc: diagnostics.rpc,
    );
  });
  await diagnostics.scenario('trusted_tls', () async {
    _expectSuccess(
      await runProbe(
        trustedTls,
        token: token,
        pinnedCertificatePems: pins,
        onRpc: diagnostics.rpc,
      ),
    );
  });

  // The host requires both this marker vertex and terminal diagnostic success.
  // The terminal event follows the complete probe, including owned cleanup.
  await diagnostics.scenario('marker', () async {
    await runProbe(
      trustedTls,
      token: token,
      keyPrefix: 'probe/grpc/ios-success/',
      pinnedCertificatePems: pins,
      onRpc: diagnostics.rpc,
    );
  });
}

void _expectSuccess(Map<String, Object> result) {
  if (result['transport'] != 'grpc-http2' ||
      result['int64'] != '922337203685477580' ||
      (result['streamRecordsBeforeCancel']! as int) < 1) {
    throw StateError('gRPC-Dart mobile contract mismatch: $result');
  }
}
