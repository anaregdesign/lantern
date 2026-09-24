import 'dart:async';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:lantern_client/src/gen/graph/v1/replication.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/replication.pb.dart'
    as replication;
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

const _responder = '0102030405060708090a0b0c0d0e0f10';
const _otherResponder = '1112131415161718191a1b1c1d1e1f20';
final _origin = List<int>.generate(16, (index) => index + 1);

final class _DirectStreamTransport implements connect.Transport {
  _DirectStreamTransport(this.unaries, this.wire);

  final connect.Transport unaries;
  final Stream<replication.SubscribeResponse> wire;

  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) => unaries.unary(spec, input, options);

  @override
  Future<connect.StreamResponse<I, O>> stream<
    I extends Object,
    O extends Object
  >(connect.Spec<I, O> spec, Stream<I> input, [connect.CallOptions? options]) {
    expect(spec.procedure, LanternReplicationService.subscribe.procedure);
    return Future.value(
      connect.StreamResponse<I, O>(
        spec,
        connect.Headers(),
        wire as Stream<O>,
        connect.Headers(),
      ),
    );
  }
}

replication.SubscribeResponse _chunk({
  int sequence = 1,
  replication.IdentityOperation operation =
      replication.IdentityOperation.IDENTITY_OPERATION_PUT_VERTEX,
  List<String> vertexKeys = const ['resident'],
  List<graph.EdgeKey> edgeKeys = const [],
}) => replication.SubscribeResponse(
  identityChunk: replication.IdentityChunk(
    origin: _origin,
    seq: Int64(sequence),
    hlc: replication.HLCTimestamp(wallNs: Int64(123), nodeId: _origin),
    operation: operation,
    isLast: true,
    vertexKeys: vertexKeys,
    edgeKeys: edgeKeys,
  ),
);

void main() {
  test(
    'checkpoint, exact chunks, plural reads, and auth stay on one pin',
    () async {
      final listening = Completer<void>();
      final paused = Completer<void>();
      final resumed = Completer<void>();
      final wire = StreamController<replication.SubscribeResponse>(
        onListen: listening.complete,
        onPause: paused.complete,
        onResume: resumed.complete,
      );
      addTearDown(wire.close);
      var tokenCalls = 0;
      var statusCalls = 0;
      var pluralCalls = 0;
      final unaries = FakeTransportBuilder()
          .unary<
            graph.GetReplicationStatusRequest,
            graph.GetReplicationStatusResponse
          >(LanternService.getReplicationStatus, (_, context) {
            statusCalls++;
            expect(
              context.requestHeaders['authorization'],
              'Bearer token-${statusCalls * 2 - 1}',
            );
            return graph.GetReplicationStatusResponse(nodeId: _responder);
          })
          .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
            LanternService.getVertices,
            (request, context) {
              pluralCalls++;
              expect(request.keys, ['resident']);
              expect(context.requestHeaders['authorization'], 'Bearer token-4');
              return graph.GetVerticesResponse(missing: request.keys);
            },
          )
          .build();
      final client = LanternClient.connect(
        Uri.parse('https://one-responder.test'),
        transport: _DirectStreamTransport(unaries, wire.stream),
        tokenProvider: () => 'token-${++tokenCalls}',
      );
      addTearDown(client.close);
      final cancellation = LanternCancellationToken();
      final session = await LanternClientIdentitySource(client).open(
        bootstrap: true,
        nextExpected: const {},
        cancellation: cancellation,
      );
      expect(session.responderId, _responder);
      final events = <OfflineIdentityEvent>[];
      final firstTwo = Completer<void>();
      final third = Completer<void>();
      final subscription = session.events.listen((event) {
        events.add(event);
        if (events.length == 2) firstTwo.complete();
        if (events.length == 3) third.complete();
      });
      await listening.future.timeout(const Duration(seconds: 2));
      wire.add(
        replication.SubscribeResponse(
          checkpoint: replication.IdentityCheckpoint(
            lastSeqPerOrigin: [MapEntry(_responder, Int64.ZERO)],
          ),
        ),
      );
      wire.add(_chunk());
      await firstTwo.future.timeout(const Duration(seconds: 2));
      expect(
        (events.first as OfflineIdentityCheckpoint)
            .cursor
            .sequences[_responder],
        BigInt.zero,
      );
      final chunk = events.last as OfflineIdentityChunk;
      expect(chunk.origin, _responder);
      expect(chunk.sequence, BigInt.one);
      expect(chunk.operation, OfflineIdentityOperation.putVertex);
      expect(chunk.keys, [const OfflineEntityKey.vertex('resident')]);

      subscription.pause();
      await paused.future.timeout(const Duration(seconds: 2));
      wire.add(
        _chunk(
          sequence: 2,
          operation: replication.IdentityOperation.IDENTITY_OPERATION_ADD_EDGE,
          vertexKeys: const [],
          edgeKeys: [graph.EdgeKey(tail: 'tail', head: 'head')],
        ),
      );
      await Future<void>.delayed(Duration.zero);
      expect(events, hasLength(2));
      subscription.resume();
      await resumed.future.timeout(const Duration(seconds: 2));
      await third.future.timeout(const Duration(seconds: 2));
      final edgeChunk = events.last as OfflineIdentityChunk;
      expect(edgeChunk.operation, OfflineIdentityOperation.addEdge);
      expect(edgeChunk.keys, [const OfflineEntityKey.edge('tail', 'head')]);
      final reads = await session.getVertices(['resident']);
      expect(reads.single, isA<OfflineRemoteMissing<Vertex>>());
      expect(pluralCalls, 1);
      expect(statusCalls, 3);
      expect(tokenCalls, 5);
      await subscription.cancel();
      await session.close();
      await session.close();
    },
  );

  test('responder switch during plural read fails closed', () async {
    var statusCalls = 0;
    final transport = FakeTransportBuilder()
        .unary<
          graph.GetReplicationStatusRequest,
          graph.GetReplicationStatusResponse
        >(
          LanternService.getReplicationStatus,
          (_, _) => graph.GetReplicationStatusResponse(
            nodeId: ++statusCalls == 3 ? _otherResponder : _responder,
          ),
        )
        .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
          LanternService.getVertices,
          (request, _) => graph.GetVerticesResponse(missing: request.keys),
        )
        .build();
    final client = LanternClient.connect(
      Uri.parse('https://one-responder.test'),
      transport: transport,
    );
    addTearDown(client.close);
    final session = await LanternClientIdentitySource(client).open(
      bootstrap: false,
      nextExpected: {_responder: BigInt.one},
      cancellation: LanternCancellationToken(),
    );
    addTearDown(session.close);
    await expectLater(
      session.getVertices(['resident']),
      throwsA(isA<OfflineChangeGapException>()),
    );
    expect(statusCalls, 3);
  });

  test('remote canceled plural read is a gap with an active caller', () async {
    final transport = FakeTransportBuilder()
        .unary<
          graph.GetReplicationStatusRequest,
          graph.GetReplicationStatusResponse
        >(
          LanternService.getReplicationStatus,
          (_, _) => graph.GetReplicationStatusResponse(nodeId: _responder),
        )
        .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
          LanternService.getVertices,
          (_, _) => throw connect.ConnectException(
            connect.Code.canceled,
            'responder canceled read',
          ),
        )
        .build();
    final client = LanternClient.connect(
      Uri.parse('https://one-responder.test'),
      transport: transport,
    );
    addTearDown(client.close);
    final cancellation = LanternCancellationToken();
    final session = await LanternClientIdentitySource(client).open(
      bootstrap: false,
      nextExpected: {_responder: BigInt.one},
      cancellation: cancellation,
    );
    addTearDown(session.close);
    await expectLater(
      session.getVertices(['resident']),
      throwsA(isA<OfflineChangeGapException>()),
    );
    expect(cancellation.isCanceled, isFalse);
  });

  test('stream retention gap and malformed frame map to gap', () async {
    for (final response in <replication.SubscribeResponse?>[
      null,
      replication.SubscribeResponse(identityChunk: replication.IdentityChunk()),
    ]) {
      final transport = FakeTransportBuilder()
          .unary<
            graph.GetReplicationStatusRequest,
            graph.GetReplicationStatusResponse
          >(
            LanternService.getReplicationStatus,
            (_, _) => graph.GetReplicationStatusResponse(nodeId: _responder),
          )
          .server<replication.SubscribeRequest, replication.SubscribeResponse>(
            LanternReplicationService.subscribe,
            (_, _) => response == null
                ? Stream<replication.SubscribeResponse>.error(
                    connect.ConnectException(
                      connect.Code.failedPrecondition,
                      'retention gap',
                    ),
                  )
                : Stream.value(response),
          )
          .build();
      final client = LanternClient.connect(
        Uri.parse('https://one-responder.test'),
        transport: transport,
      );
      final session = await LanternClientIdentitySource(client).open(
        bootstrap: false,
        nextExpected: {_responder: BigInt.one},
        cancellation: LanternCancellationToken(),
      );
      await expectLater(
        session.events.toList(),
        throwsA(isA<OfflineChangeGapException>()),
      );
      await session.close();
      await client.close();
    }
  });

  test('remote canceled stream is a gap with an active caller', () async {
    final transport = FakeTransportBuilder()
        .unary<
          graph.GetReplicationStatusRequest,
          graph.GetReplicationStatusResponse
        >(
          LanternService.getReplicationStatus,
          (_, _) => graph.GetReplicationStatusResponse(nodeId: _responder),
        )
        .server<replication.SubscribeRequest, replication.SubscribeResponse>(
          LanternReplicationService.subscribe,
          (_, _) => Stream<replication.SubscribeResponse>.error(
            connect.ConnectException(
              connect.Code.canceled,
              'responder canceled stream',
            ),
          ),
        )
        .build();
    final client = LanternClient.connect(
      Uri.parse('https://one-responder.test'),
      transport: transport,
    );
    addTearDown(client.close);
    final cancellation = LanternCancellationToken();
    final session = await LanternClientIdentitySource(client).open(
      bootstrap: false,
      nextExpected: {_responder: BigInt.one},
      cancellation: cancellation,
    );
    addTearDown(session.close);
    await expectLater(
      session.events.toList(),
      throwsA(isA<OfflineChangeGapException>()),
    );
    expect(cancellation.isCanceled, isFalse);
  });

  test(
    'caller cancellation releases stream and rejects late plural reads',
    () async {
      final listening = Completer<void>();
      final canceled = Completer<void>();
      final wire = StreamController<replication.SubscribeResponse>(
        onListen: listening.complete,
        onCancel: canceled.complete,
      );
      addTearDown(wire.close);
      final unaries = FakeTransportBuilder()
          .unary<
            graph.GetReplicationStatusRequest,
            graph.GetReplicationStatusResponse
          >(
            LanternService.getReplicationStatus,
            (_, _) => graph.GetReplicationStatusResponse(nodeId: _responder),
          )
          .build();
      final client = LanternClient.connect(
        Uri.parse('https://one-responder.test'),
        transport: _DirectStreamTransport(unaries, wire.stream),
      );
      addTearDown(client.close);
      final cancellation = LanternCancellationToken();
      final session = await LanternClientIdentitySource(client).open(
        bootstrap: true,
        nextExpected: const {},
        cancellation: cancellation,
      );
      final subscription = session.events.listen((_) {});
      await listening.future.timeout(const Duration(seconds: 2));
      cancellation.cancel();
      await canceled.future.timeout(const Duration(seconds: 2));
      await expectLater(
        session.getVertices(['resident']),
        throwsA(isA<OfflineCanceledException>()),
      );
      await subscription.cancel();
      await session.close();
    },
  );

  test(
    'authentication failure, canceled open, and close before listen',
    () async {
      final transport = FakeTransportBuilder()
          .unary<
            graph.GetReplicationStatusRequest,
            graph.GetReplicationStatusResponse
          >(
            LanternService.getReplicationStatus,
            (_, _) => throw connect.ConnectException(
              connect.Code.unauthenticated,
              'expired',
            ),
          )
          .build();
      final client = LanternClient.connect(
        Uri.parse('https://one-responder.test'),
        transport: transport,
      );
      addTearDown(client.close);
      await expectLater(
        LanternClientIdentitySource(client).open(
          bootstrap: false,
          nextExpected: const {},
          cancellation: LanternCancellationToken(),
        ),
        throwsA(
          isA<OfflineRemoteFailure>().having(
            (failure) => failure.kind,
            'kind',
            OfflineRemoteErrorKind.unauthenticated,
          ),
        ),
      );
      final canceled = LanternCancellationToken()..cancel();
      await expectLater(
        LanternClientIdentitySource(client).open(
          bootstrap: false,
          nextExpected: const {},
          cancellation: canceled,
        ),
        throwsA(isA<OfflineCanceledException>()),
      );

      final good = LanternClient.connect(
        Uri.parse('https://one-responder.test'),
        transport: FakeTransportBuilder()
            .unary<
              graph.GetReplicationStatusRequest,
              graph.GetReplicationStatusResponse
            >(
              LanternService.getReplicationStatus,
              (_, _) => graph.GetReplicationStatusResponse(nodeId: _responder),
            )
            .build(),
      );
      addTearDown(good.close);
      final session = await LanternClientIdentitySource(good).open(
        bootstrap: true,
        nextExpected: const {},
        cancellation: LanternCancellationToken(),
      );
      await session.close();
    },
  );
}
