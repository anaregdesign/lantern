import 'dart:io';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/io.dart' as connect_io;
import 'package:connectrpc/protobuf.dart';
import 'package:connectrpc/protocol/connect.dart' as protocol;
import 'package:fixnum/fixnum.dart';

import 'src/gen/graph/v1/graph.connect.client.dart';
import 'src/gen/graph/v1/graph.pb.dart';

typedef ProbeRpcObserver =
    void Function({
      required String method,
      required String state,
      String? code,
      StackTrace? stackTrace,
    });

/// A bounded status label; exception messages and metadata are never included.
String probeErrorCode(Object error) {
  if (error is connect.ConnectException) {
    return error.code == connect.Code.canceled ? 'cancelled' : error.code.name;
  }
  if (error is StateError || error is AssertionError) return 'assertion';
  if (error is FormatException) return 'configuration';
  return 'unknown';
}

Future<Map<String, Object>> runProbe(
  Uri endpoint, {
  String? token,
  String? caPath,
  List<int>? trustedCertificateBytes,
  HttpClient? httpClient,
  String keyPrefix = 'probe/connect/',
  Iterable<String> pinnedCertificatePems = const <String>[],
  ProbeRpcObserver? onRpc,
}) async {
  final securityContext =
      caPath == null && trustedCertificateBytes == null
          ? null
          : SecurityContext(withTrustedRoots: false);
  if (caPath != null) {
    securityContext!.setTrustedCertificates(caPath);
  }
  if (trustedCertificateBytes != null) {
    securityContext!.setTrustedCertificatesBytes(trustedCertificateBytes);
  }
  final ownsHttpClient = httpClient == null;
  final ioClient = httpClient ?? HttpClient(context: securityContext);
  final normalizedPins = pinnedCertificatePems.map(_normalizePem).toSet();
  if (normalizedPins.isNotEmpty) {
    ioClient.badCertificateCallback =
        (certificate, host, _) =>
            (host == endpoint.host || host == endpoint.authority) &&
            normalizedPins.contains(_normalizePem(certificate.pem));
  }
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
    final key = '$keyPrefix${DateTime.now().microsecondsSinceEpoch}';
    var sawHeader = false;
    var sawTrailer = false;
    await _observeRpc(
      'PutVertex',
      onRpc,
      () => client.putVertex(
        PutVertexRequest(
          vertex: Vertex(key: key, int64: Int64.parseInt('922337203685477580')),
        ),
        headers: headers,
        signal: connect.TimeoutSignal(const Duration(seconds: 5)),
        onHeader: (_) => sawHeader = true,
        onTrailer: (_) => sawTrailer = true,
      ),
    );
    final read = await _observeRpc('GetVertex', onRpc, () async {
      final read = await client.getVertex(
        GetVertexRequest(key: key),
        headers: headers,
        signal: connect.TimeoutSignal(const Duration(seconds: 5)),
      );
      if (read.vertex.int64.toString() != '922337203685477580') {
        throw StateError('int64 round-trip mismatch: ${read.vertex.int64}');
      }
      return read;
    });

    final records = await _observeRpc('BackupSnapshot', onRpc, () async {
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
      return records;
    });
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
      await _observeRpc('ChannelShutdown', onRpc, () async {
        ioClient.close(force: true);
      });
    }
  }
}

Future<T> _observeRpc<T>(
  String method,
  ProbeRpcObserver? observer,
  Future<T> Function() operation,
) async {
  _notify(observer, method, 'start');
  try {
    final result = await operation();
    _notify(observer, method, 'success');
    return result;
  } catch (error, stackTrace) {
    _notify(observer, method, 'failure', probeErrorCode(error), stackTrace);
    rethrow;
  }
}

void _notify(
  ProbeRpcObserver? observer,
  String method,
  String state, [
  String? code,
  StackTrace? stackTrace,
]) {
  try {
    observer?.call(
      method: method,
      state: state,
      code: code,
      stackTrace: stackTrace,
    );
  } catch (_) {
    // Diagnostics must not change the RPC outcome or prevent owned cleanup.
  }
}

String _normalizePem(String pem) => pem.replaceAll(RegExp(r'\s'), '');
