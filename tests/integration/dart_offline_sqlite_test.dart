// Run from sdks/dart/offline_sqlite with flutter test and a real h2c server.
import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

void main() {
  sqfliteFfiInit();
  test(
    'response-dropping proxy loses committed PutVertex and PutEdge responses',
    () async {
      final endpointValue =
          Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
      if (endpointValue == null || endpointValue.isEmpty) {
        markTestSkipped('set LANTERN_DART_REAL_WIRE_ENDPOINT');
        return;
      }
      final endpoint = Uri.parse(endpointValue);
      final serverClient = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        idempotentAdds: true,
      );
      addTearDown(serverClient.close);
      await serverClient.ping();

      final proxy = await _ResponseDroppingProxy.bind(
        endpoint,
        drops: const <String, int>{'PutVertices': 1, 'PutEdges': 1},
      );
      addTearDown(proxy.close);
      final client = LanternClient.connect(proxy.endpoint, allowInsecure: true);
      addTearDown(client.close);
      await client.ping();

      final online = LanternClientOfflineRemote(client);
      final directory = await Directory.systemTemp.createTemp('sqlite-wire-');
      addTearDown(() => directory.delete(recursive: true));
      final path = '${directory.path}/offline.db';
      var store = await SqliteOfflineStore.open(
        path: path,
        databaseFactory: databaseFactoryFfi,
      );
      addTearDown(() => store.close());
      final enqueueNow = DateTime.now().toUtc();
      final repository = OfflineLanternRepository(
        store: store,
        remote: online,
        config: OfflineConfig(
          clock: () => enqueueNow,
          jitter: (ceiling) => ceiling,
          baseRetryDelay: const Duration(seconds: 1),
        ),
      );
      addTearDown(repository.dispose);
      final prefix =
          'dart-sqlite-wire:${DateTime.now().microsecondsSinceEpoch}:';
      final vertexKey = '${prefix}vertex';
      final tail = '${prefix}tail';
      final head = '${prefix}head';
      await repository.putVertex(
        partitionId: 'wire',
        input: VertexInput(
          key: vertexKey,
          value: VertexValue.string('committed'),
          expiresIn: const Duration(minutes: 5),
        ),
      );
      await repository.putEdge(
        partitionId: 'wire',
        input: EdgeInput(
          tail: tail,
          head: head,
          weight: 0.75,
          expiresIn: const Duration(minutes: 5),
        ),
      );
      expect(await repository.drain('wire'), 0);
      expect(proxy.forwarded('PutVertices'), 1);
      expect(proxy.forwarded('PutEdges'), 1);
      expect(proxy.dropped('PutVertices'), 1);
      expect(proxy.dropped('PutEdges'), 1);
      final beforeRestart = await store.transaction(
        (transaction) async => await transaction.outbox('wire'),
      );
      expect(beforeRestart, hasLength(2));
      expect(
        beforeRestart.map((record) => record.attemptCount),
        everyElement(1),
      );
      expect(
        beforeRestart.map((record) => record.diagnosticCode),
        everyElement('unavailable'),
      );
      final firstVertex = beforeRestart
          .map((record) => record.intent)
          .whereType<OfflinePutVertexIntent>()
          .single;
      final firstEdge = beforeRestart
          .map((record) => record.intent)
          .whereType<OfflinePutEdgeIntent>()
          .single;
      expect(
        (await serverClient.getVertex(vertexKey)).value,
        isA<StringValue>().having((value) => value.value, 'value', 'committed'),
        reason: 'the upstream server committed before the proxy disconnected',
      );
      expect(
        (await serverClient.getEdge(EdgeRef(tail, head))).weight,
        Float32Value(0.75).value,
      );

      await repository.dispose();
      await store.close();
      store = await SqliteOfflineStore.open(
        path: path,
        databaseFactory: databaseFactoryFfi,
      );
      final restarted = OfflineLanternRepository(
        store: store,
        remote: online,
        config: OfflineConfig(
          clock: () => enqueueNow.add(const Duration(seconds: 2)),
          jitter: (_) => Duration.zero,
        ),
      );
      addTearDown(restarted.dispose);
      expect(await restarted.drain('wire'), 2);
      expect(proxy.forwarded('PutVertices'), 2);
      expect(proxy.forwarded('PutEdges'), 2);

      final vertex = await serverClient.getVertex(vertexKey);
      expect((vertex.value as StringValue).value, 'committed');
      expect(
        vertex.expiration,
        firstVertex.vertex.expiration,
        reason: 'replay must not rebase the once-resolved Vertex TTL',
      );
      final edge = await serverClient.getEdge(EdgeRef(tail, head));
      expect(edge.weight, Float32Value(0.75).value);
      expect(
        edge.expiration,
        firstEdge.edge.expiration,
        reason: 'replay must not rebase the once-resolved Edge TTL',
      );
      expect(await restarted.listPending('wire'), isEmpty);
    },
  );
}

final class _ResponseDroppingProxy {
  _ResponseDroppingProxy._(
    this._server,
    this._upstreamEndpoint,
    Map<String, int> drops,
  ) : _remainingDrops = Map<String, int>.of(drops) {
    _upstream.autoUncompress = false;
    _server.listen((request) => unawaited(_forward(request)));
  }

  static Future<_ResponseDroppingProxy> bind(
    Uri upstreamEndpoint, {
    required Map<String, int> drops,
  }) async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    return _ResponseDroppingProxy._(server, upstreamEndpoint, drops);
  }

  final HttpServer _server;
  final Uri _upstreamEndpoint;
  final HttpClient _upstream = HttpClient();
  final Map<String, int> _remainingDrops;
  final Map<String, int> _forwarded = <String, int>{};
  final Map<String, int> _dropped = <String, int>{};

  Uri get endpoint =>
      Uri(scheme: 'http', host: _server.address.host, port: _server.port);

  int forwarded(String rpc) => _forwarded[rpc] ?? 0;

  int dropped(String rpc) => _dropped[rpc] ?? 0;

  Future<void> close() async {
    await _server.close(force: true);
    _upstream.close(force: true);
  }

  Future<void> _forward(HttpRequest downstream) async {
    final rpc = downstream.uri.pathSegments.isEmpty
        ? ''
        : downstream.uri.pathSegments.last;
    _forwarded[rpc] = (_forwarded[rpc] ?? 0) + 1;
    try {
      final target = _upstreamEndpoint.resolveUri(downstream.uri);
      final upstreamRequest = await _upstream.openUrl(
        downstream.method,
        target,
      );
      _copyHeaders(downstream.headers, upstreamRequest.headers);
      await upstreamRequest.addStream(downstream);
      final upstreamResponse = await upstreamRequest.close();
      final responseBytes = await upstreamResponse.fold<BytesBuilder>(
        BytesBuilder(copy: false),
        (builder, bytes) => builder..add(bytes),
      );

      final remaining = _remainingDrops[rpc] ?? 0;
      if (remaining > 0) {
        _remainingDrops[rpc] = remaining - 1;
        _dropped[rpc] = (_dropped[rpc] ?? 0) + 1;
        final socket = await downstream.response.detachSocket(
          writeHeaders: false,
        );
        socket.destroy();
        return;
      }

      downstream.response.statusCode = upstreamResponse.statusCode;
      _copyHeaders(upstreamResponse.headers, downstream.response.headers);
      downstream.response.add(responseBytes.takeBytes());
      await downstream.response.close();
    } on Object {
      try {
        downstream.response.statusCode = HttpStatus.badGateway;
        await downstream.response.close();
      } on Object {
        // The deliberately disconnected response has no writable downstream.
      }
    }
  }

  static void _copyHeaders(HttpHeaders source, HttpHeaders target) {
    const ignored = <String>{
      'connection',
      'content-length',
      'host',
      'keep-alive',
      'proxy-authenticate',
      'proxy-authorization',
      'te',
      'trailer',
      'transfer-encoding',
      'upgrade',
    };
    source.forEach((name, values) {
      if (!ignored.contains(name.toLowerCase())) {
        target.set(name, values);
      }
    });
  }
}

final class _CommittedResponseGateRemote implements OfflineRemote {
  _CommittedResponseGateRemote(this.delegate);

  final OfflineRemote delegate;
  final Completer<void> serverCommitted = Completer<void>();

  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.getEdge(edge, cancellation: cancellation);

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) => delegate.getVertex(key, cancellation: cancellation);

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) =>
      delegate.probe(cancellation: cancellation);

  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.putEdge(edge, cancellation: cancellation);

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    final outcome = await delegate.putVertex(
      vertex,
      cancellation: cancellation,
    );
    if (!serverCommitted.isCompleted) serverCommitted.complete();
    final canceled = Completer<void>();
    final removeCancellation = cancellation?.listen((_) {
      if (!canceled.isCompleted) {
        canceled.completeError(
          const OfflineRemoteFailure(
            OfflineRemoteErrorKind.canceled,
            OfflineCanceledException(),
          ),
        );
      }
    });
    try {
      await canceled.future;
    } finally {
      removeCancellation?.call();
    }
    return outcome;
  }
}

final class _FailingAfterRemote implements OfflineRemote {
  _FailingAfterRemote(this.delegate, {required this.succeedBefore});

  final OfflineRemote delegate;
  final int succeedBefore;
  var putVertexCalls = 0;

  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.getEdge(edge, cancellation: cancellation);

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) => delegate.getVertex(key, cancellation: cancellation);

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) =>
      delegate.probe(cancellation: cancellation);

  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.putEdge(edge, cancellation: cancellation);

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) {
    putVertexCalls += 1;
    if (putVertexCalls > succeedBefore) {
      throw OfflineRemoteFailure(
        OfflineRemoteErrorKind.unavailable,
        StateError('simulated outage'),
      );
    }
    return delegate.putVertex(vertex, cancellation: cancellation);
  }
}
