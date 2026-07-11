import 'dart:io';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/io.dart' as connect_io;
import 'package:connectrpc/protobuf.dart';
import 'package:connectrpc/protocol/connect.dart' as protocol;
import 'package:fixnum/fixnum.dart';

import 'src/gen/graph/v1/graph.connect.client.dart';
import 'src/gen/graph/v1/graph.pb.dart';

Future<Map<String, Object>> runProbe(
  Uri endpoint, {
  String? token,
  String? caPath,
  HttpClient? httpClient,
}) async {
  final securityContext =
      caPath == null
          ? null
          : (SecurityContext(withTrustedRoots: false)
            ..setTrustedCertificates(caPath));
  final ownsHttpClient = httpClient == null;
  final ioClient = httpClient ?? HttpClient(context: securityContext);
  try {
    final transport = protocol.Transport(
      baseUrl: endpoint.toString(),
      codec: const ProtoCodec(),
      httpClient: connect_io.createHttpClient(ioClient),
    );
    final client = LanternServiceClient(transport);
    final headers = connect.Headers();
    if (token != null) {
      headers['authorization'] = 'Bearer $token';
    }
    final key = 'probe/connect/${DateTime.now().microsecondsSinceEpoch}';
    var sawHeader = false;
    var sawTrailer = false;
    await client.putVertex(
      PutVertexRequest(
        vertex: Vertex(key: key, int64: Int64.parseInt('922337203685477580')),
      ),
      headers: headers,
      signal: connect.TimeoutSignal(const Duration(seconds: 5)),
      onHeader: (_) => sawHeader = true,
      onTrailer: (_) => sawTrailer = true,
    );
    final read = await client.getVertex(
      GetVertexRequest(key: key),
      headers: headers,
      signal: connect.TimeoutSignal(const Duration(seconds: 5)),
    );
    if (read.vertex.int64.toString() != '922337203685477580') {
      throw StateError('int64 round-trip mismatch: ${read.vertex.int64}');
    }

    final streamSignal = connect.CancelableSignal();
    var records = 0;
    await for (final _ in client.backupSnapshot(
      BackupSnapshotRequest(vertexPrefix: 'probe/connect/'),
      headers: headers,
      signal: streamSignal,
    )) {
      records++;
      streamSignal.cancel();
      break;
    }
    if (records == 0) {
      throw StateError('backup stream returned no records');
    }
    return <String, Object>{
      'transport': 'connect-http1',
      'endpoint': endpoint.toString(),
      'int64': read.vertex.int64.toString(),
      'headerCallback': sawHeader,
      'trailerCallback': sawTrailer,
      'streamRecordsBeforeCancel': records,
    };
  } finally {
    if (ownsHttpClient) {
      ioClient.close(force: true);
    }
  }
}
