import 'dart:io';

import 'package:fixnum/fixnum.dart';
import 'package:grpc/grpc.dart';

import 'src/gen/graph/v1/graph.pbgrpc.dart';

Future<Map<String, Object>> runProbe(
  Uri endpoint, {
  String? token,
  String? caPath,
  ClientChannel? clientChannel,
  String keyPrefix = 'probe/grpc/',
  Iterable<String> pinnedCertificatePems = const <String>[],
}) async {
  final normalizedPins = pinnedCertificatePems.map(_normalizePem).toSet();
  final credentials = endpoint.scheme == 'https'
      ? ChannelCredentials.secure(
          certificates: caPath == null ? null : File(caPath).readAsBytesSync(),
          onBadCertificate: normalizedPins.isEmpty
              ? null
              : (certificate, host) =>
                    (host == endpoint.host || host == endpoint.authority) &&
                    normalizedPins.contains(_normalizePem(certificate.pem)),
        )
      : const ChannelCredentials.insecure();
  final ownsChannel = clientChannel == null;
  final channel =
      clientChannel ??
      ClientChannel(
        endpoint.host,
        port: endpoint.hasPort
            ? endpoint.port
            : (endpoint.scheme == 'https' ? 443 : 80),
        options: ChannelOptions(credentials: credentials),
      );
  final metadata = token == null
      ? const <String, String>{}
      : <String, String>{'authorization': 'Bearer $token'};
  final options = CallOptions(
    metadata: metadata,
    timeout: const Duration(seconds: 5),
  );
  final client = LanternServiceClient(channel, options: options);
  final key = '$keyPrefix${DateTime.now().microsecondsSinceEpoch}';
  try {
    await client.putVertex(
      PutVertexRequest(
        vertex: Vertex(key: key, int64: Int64.parseInt('922337203685477580')),
      ),
    );
    final read = await client.getVertex(GetVertexRequest(key: key));
    if (read.vertex.int64.toString() != '922337203685477580') {
      throw StateError('int64 round-trip mismatch: ${read.vertex.int64}');
    }

    final stream = client.backupSnapshot(
      BackupSnapshotRequest(vertexPrefix: 'probe/grpc/'),
    );
    var records = 0;
    await for (final _ in stream) {
      records++;
      await stream.cancel();
      break;
    }
    if (records == 0) {
      throw StateError('backup stream returned no records');
    }
    return <String, Object>{
      'transport': 'grpc-http2',
      'endpoint': endpoint.toString(),
      'int64': read.vertex.int64.toString(),
      'streamRecordsBeforeCancel': records,
    };
  } finally {
    if (ownsChannel) {
      await channel.shutdown();
    }
  }
}

String _normalizePem(String pem) => pem.replaceAll(RegExp(r'\s'), '');
