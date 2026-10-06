import 'dart:io';

import 'package:fixnum/fixnum.dart';
import 'package:grpc/grpc.dart';

import 'src/gen/graph/v1/graph.pbgrpc.dart';

typedef ProbeRpcObserver =
    void Function({
      required String method,
      required String state,
      String? code,
      StackTrace? stackTrace,
    });

/// A bounded status label; exception messages and metadata are never included.
String probeErrorCode(Object error) {
  if (error is GrpcError) {
    const codes = <String>[
      'ok',
      'cancelled',
      'unknown',
      'invalid_argument',
      'deadline_exceeded',
      'not_found',
      'already_exists',
      'permission_denied',
      'resource_exhausted',
      'failed_precondition',
      'aborted',
      'out_of_range',
      'unimplemented',
      'internal',
      'unavailable',
      'data_loss',
      'unauthenticated',
    ];
    return error.code >= 0 && error.code < codes.length
        ? codes[error.code]
        : 'unknown';
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
  ClientChannel? clientChannel,
  String keyPrefix = 'probe/grpc/',
  Iterable<String> pinnedCertificatePems = const <String>[],
  ProbeRpcObserver? onRpc,
}) async {
  final normalizedPins = pinnedCertificatePems.map(_normalizePem).toSet();
  final credentials = endpoint.scheme == 'https'
      ? ChannelCredentials.secure(
          certificates:
              trustedCertificateBytes ??
              (caPath == null ? null : File(caPath).readAsBytesSync()),
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
    await _observeRpc(
      'PutVertex',
      onRpc,
      () => client.putVertex(
        PutVertexRequest(
          vertex: Vertex(key: key, int64: Int64.parseInt('922337203685477580')),
        ),
      ),
    );
    final read = await _observeRpc('GetVertex', onRpc, () async {
      final read = await client.getVertex(GetVertexRequest(key: key));
      if (read.vertex.int64.toString() != '922337203685477580') {
        throw StateError('int64 round-trip mismatch: ${read.vertex.int64}');
      }
      return read;
    });

    final records = await _observeRpc('BackupSnapshot', onRpc, () async {
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
      return records;
    });
    return <String, Object>{
      'transport': 'grpc-http2',
      'endpoint': endpoint.toString(),
      'int64': read.vertex.int64.toString(),
      'streamRecordsBeforeCancel': records,
    };
  } finally {
    if (ownsChannel) {
      await _observeRpc('ChannelShutdown', onRpc, channel.shutdown);
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
