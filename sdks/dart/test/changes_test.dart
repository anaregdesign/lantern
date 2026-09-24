import 'dart:async';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:lantern_client/src/gen/graph/v1/replication.connect.spec.dart'
    as replication_spec;
import 'package:lantern_client/src/gen/graph/v1/replication.pb.dart'
    as replication;
import 'package:test/test.dart';

const _origin = '0102030405060708090a0b0c0d0e0f10';
final _originBytes = List<int>.generate(16, (index) => index + 1);
final _maxUint64 = (BigInt.one << 64) - BigInt.one;

replication.SubscribeResponse _chunk({
  replication.IdentityOperation operation =
      replication.IdentityOperation.IDENTITY_OPERATION_PUT_VERTEX,
  Iterable<String> vertexKeys = const ['v'],
  Iterable<graph.EdgeKey> edgeKeys = const [],
  int chunkIndex = 0,
  bool isLast = true,
  Int64? sequence,
}) => replication.SubscribeResponse(
  identityChunk: replication.IdentityChunk(
    origin: _originBytes,
    seq: sequence ?? Int64.ONE,
    hlc: replication.HLCTimestamp(
      wallNs: Int64(123),
      logical: 4,
      nodeId: _originBytes,
    ),
    operation: operation,
    chunkIndex: chunkIndex,
    isLast: isLast,
    vertexKeys: vertexKeys,
    edgeKeys: edgeKeys,
  ),
);

LanternClient _client(
  connect.Transport transport, {
  TokenProvider? tokenProvider,
  Duration? defaultTimeout,
}) => LanternClient.connect(
  Uri.parse('https://lantern.test'),
  transport: transport,
  tokenProvider: tokenProvider,
  defaultTimeout: defaultTimeout,
);

final class _DirectIdentityStreamTransport implements connect.Transport {
  _DirectIdentityStreamTransport(this.responses);

  final Stream<replication.SubscribeResponse> responses;

  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) => throw UnimplementedError();

  @override
  Future<connect.StreamResponse<I, O>> stream<
    I extends Object,
    O extends Object
  >(connect.Spec<I, O> spec, Stream<I> input, [connect.CallOptions? options]) {
    expect(
      spec.procedure,
      replication_spec.LanternReplicationService.subscribe.procedure,
    );
    return Future.value(
      connect.StreamResponse<I, O>(
        spec,
        connect.Headers(),
        responses as Stream<O>,
        connect.Headers(),
      ),
    );
  }
}

void main() {
  test(
    'NEXT cursor is immutable and rejects malformed or exhausted values',
    () {
      final input = {_origin: BigInt.one};
      final cursor = IdentityNextCursor(input);
      input[_origin] = BigInt.two;
      expect(cursor.nextSequences[_origin], BigInt.one);
      expect(
        () => cursor.nextSequences[_origin] = BigInt.two,
        throwsUnsupportedError,
      );
      expect(
        IdentityNextCursor.fromLastApplied({
          _origin: _maxUint64 - BigInt.one,
        }).nextSequences[_origin],
        _maxUint64,
      );
      for (final invalid in [BigInt.zero, _maxUint64 + BigInt.one]) {
        expect(
          () => IdentityNextCursor({_origin: invalid}),
          throwsA(isA<LanternInvalidArgumentException>()),
        );
      }
      expect(
        () => IdentityNextCursor.fromLastApplied({_origin: _maxUint64}),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      for (final invalid in [
        _origin.toUpperCase(),
        '00000000000000000000000000000000',
        'invalid',
      ]) {
        expect(
          () => IdentityNextCursor({invalid: BigInt.one}),
          throwsA(isA<LanternInvalidArgumentException>()),
        );
      }
    },
  );

  test(
    'bootstrap maps immutable typed frames with auth and no unary deadline',
    () async {
      var tokenCalls = 0;
      replication.SubscribeRequest? request;
      final transport = FakeTransportBuilder()
          .server<replication.SubscribeRequest, replication.SubscribeResponse>(
            replication_spec.LanternReplicationService.subscribe,
            (input, context) async* {
              request = input;
              expect(context.requestHeaders['authorization'], 'Bearer token-1');
              expect(context.signal.deadline, isNull);
              yield replication.SubscribeResponse(
                checkpoint: replication.IdentityCheckpoint(
                  lastSeqPerOrigin: [MapEntry(_origin, Int64(7))],
                ),
              );
              yield _chunk(
                operation:
                    replication.IdentityOperation.IDENTITY_OPERATION_ADD_EDGE,
                vertexKeys: const [],
                edgeKeys: [graph.EdgeKey(tail: 'tail', head: 'head')],
                sequence: Int64(8),
              );
            },
          )
          .build();
      final client = _client(
        transport,
        tokenProvider: () => 'token-${++tokenCalls}',
        defaultTimeout: const Duration(milliseconds: 1),
      );
      addTearDown(client.close);

      final frames = await client
          .subscribeIdentity(bootstrap: true)
          .take(2)
          .toList();
      expect(request!.bootstrap, isTrue);
      expect(
        request!.projection,
        replication.SubscribeProjection.SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
      );
      expect(request!.fromSeqPerOrigin, isEmpty);
      expect(tokenCalls, 1);
      final checkpoint = frames[0] as IdentityCheckpointFrame;
      expect(checkpoint.lastSequences[_origin], BigInt.from(7));
      expect(
        () => checkpoint.lastSequences[_origin] = BigInt.one,
        throwsUnsupportedError,
      );
      final chunk = frames[1] as IdentityChunkFrame;
      expect(chunk.origin, _origin);
      expect(chunk.sequence, BigInt.from(8));
      expect(chunk.hlc.wallNanoseconds, BigInt.from(123));
      expect(chunk.hlc.logical, 4);
      expect(chunk.operation, IdentityOperation.addEdge);
      expect(chunk.edgeKeys, [const EdgeRef('tail', 'head')]);
      expect(chunk.vertexKeys, isEmpty);
      expect(
        () => chunk.edgeKeys.add(const EdgeRef('x', 'y')),
        throwsUnsupportedError,
      );
    },
  );

  test(
    'resume sends exact NEXT cursor and honors an explicit deadline',
    () async {
      replication.SubscribeRequest? request;
      final transport = FakeTransportBuilder()
          .server<replication.SubscribeRequest, replication.SubscribeResponse>(
            replication_spec.LanternReplicationService.subscribe,
            (input, context) async* {
              request = input;
              expect(context.signal.deadline, isNotNull);
              yield _chunk(sequence: Int64(9));
            },
          )
          .build();
      final client = _client(transport);
      addTearDown(client.close);
      final frame = await client
          .subscribeIdentity(
            cursor: IdentityNextCursor({_origin: BigInt.from(9)}),
            options: LanternCallOptions(timeout: const Duration(seconds: 3)),
          )
          .first;
      expect(request!.bootstrap, isFalse);
      expect(request!.fromSeqPerOrigin[_origin], Int64(9));
      expect((frame as IdentityChunkFrame).vertexKeys, ['v']);
      expect(
        () => client.subscribeIdentity(
          bootstrap: true,
          cursor: IdentityNextCursor({_origin: BigInt.one}),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    },
  );

  test(
    'uint64 high-bit sequence survives request and stream decoding',
    () async {
      final maxWireValue = Int64.fromBytesBigEndian(List<int>.filled(8, 0xff));
      final transport = FakeTransportBuilder()
          .server<replication.SubscribeRequest, replication.SubscribeResponse>(
            replication_spec.LanternReplicationService.subscribe,
            (input, _) {
              expect(input.fromSeqPerOrigin[_origin], maxWireValue);
              return Stream.value(_chunk(sequence: maxWireValue));
            },
          )
          .build();
      final client = _client(transport);
      addTearDown(client.close);

      final frame = await client
          .subscribeIdentity(cursor: IdentityNextCursor({_origin: _maxUint64}))
          .first;
      expect((frame as IdentityChunkFrame).sequence, _maxUint64);
    },
  );

  test(
    'identity facade forwards pause and resume to the wire stream',
    () async {
      final listening = Completer<void>();
      final paused = Completer<void>();
      final resumed = Completer<void>();
      final source = StreamController<replication.SubscribeResponse>(
        onListen: listening.complete,
        onPause: paused.complete,
        onResume: resumed.complete,
      );
      final client = _client(_DirectIdentityStreamTransport(source.stream));
      addTearDown(client.close);
      final delivered = <IdentityFrame>[];
      final first = Completer<void>();
      final second = Completer<void>();
      final subscription = client.subscribeIdentity().listen((frame) {
        delivered.add(frame);
        if (delivered.length == 1) first.complete();
        if (delivered.length == 2) second.complete();
      });
      await listening.future.timeout(const Duration(seconds: 2));
      source.add(_chunk());
      await first.future.timeout(const Duration(seconds: 2));

      subscription.pause();
      await paused.future.timeout(const Duration(seconds: 2));
      source.add(_chunk(sequence: Int64(2)));
      await Future<void>.delayed(Duration.zero);
      expect(delivered, hasLength(1));

      subscription.resume();
      await resumed.future.timeout(const Duration(seconds: 2));
      await second.future.timeout(const Duration(seconds: 2));
      expect(delivered, everyElement(isA<IdentityChunkFrame>()));
      await subscription.cancel();
      await source.close();
    },
  );

  test('identity cancellation consumes transport cleanup errors', () async {
    final listening = Completer<void>();
    final source = StreamController<replication.SubscribeResponse>(
      onListen: listening.complete,
      onCancel: () async => throw StateError('transport cleanup failed'),
    );
    final client = _client(_DirectIdentityStreamTransport(source.stream));
    addTearDown(client.close);
    final first = Completer<void>();
    final subscription = client.subscribeIdentity().listen((_) {
      first.complete();
    });
    await listening.future.timeout(const Duration(seconds: 2));
    source.add(_chunk());
    await first.future.timeout(const Duration(seconds: 2));

    await subscription.cancel();
    await Future<void>.delayed(Duration.zero);
  });

  test('unexpected full Mutation and malformed chunks fail closed', () async {
    for (final bad in [
      replication.SubscribeResponse(mutation: replication.Mutation()),
      replication.SubscribeResponse(),
      _chunk(sequence: Int64.ZERO),
      _chunk(
        operation: replication.IdentityOperation.IDENTITY_OPERATION_UNSPECIFIED,
      ),
      _chunk(
        operation: replication.IdentityOperation.IDENTITY_OPERATION_ADD_EDGE,
      ),
      _chunk(vertexKeys: const ['']),
      _chunk(
        operation: replication.IdentityOperation.IDENTITY_OPERATION_ADD_EDGE,
        vertexKeys: const [],
        edgeKeys: [graph.EdgeKey(tail: '', head: 'head')],
      ),
      _chunk(
        operation: replication.IdentityOperation.IDENTITY_OPERATION_ADD_EDGE,
        vertexKeys: const [],
        edgeKeys: [graph.EdgeKey(tail: 'tail', head: '')],
      ),
    ]) {
      final transport = FakeTransportBuilder()
          .server<replication.SubscribeRequest, replication.SubscribeResponse>(
            replication_spec.LanternReplicationService.subscribe,
            (_, _) => Stream.value(bad),
          )
          .build();
      final client = _client(transport);
      await expectLater(
        client.subscribeIdentity().toList(),
        throwsA(isA<LanternInternalException>()),
      );
      await client.close();
    }
  });

  test('receipt-only marker has a final zero-key identity frame', () async {
    final marker = _chunk(
      operation: replication.IdentityOperation.IDENTITY_OPERATION_RECEIPT_ONLY,
      vertexKeys: const [],
    );
    final transport = FakeTransportBuilder()
        .server<replication.SubscribeRequest, replication.SubscribeResponse>(
          replication_spec.LanternReplicationService.subscribe,
          (_, _) => Stream.value(marker),
        )
        .build();
    final client = _client(transport);
    final frame = await client.subscribeIdentity().first as IdentityChunkFrame;
    expect(frame.operation, IdentityOperation.receiptOnly);
    expect(frame.isLast, isTrue);
    expect(frame.vertexKeys, isEmpty);
    expect(frame.edgeKeys, isEmpty);
    await client.close();

    final badIndex = _chunk(
      operation: replication.IdentityOperation.IDENTITY_OPERATION_RECEIPT_ONLY,
      vertexKeys: const [],
    );
    badIndex.identityChunk.chunkIndex = 1;
    for (final bad in [
      _chunk(
        operation:
            replication.IdentityOperation.IDENTITY_OPERATION_RECEIPT_ONLY,
      ),
      _chunk(
        operation:
            replication.IdentityOperation.IDENTITY_OPERATION_RECEIPT_ONLY,
        vertexKeys: const [],
        edgeKeys: [graph.EdgeKey(tail: 'tail', head: 'head')],
      ),
      _chunk(
        operation:
            replication.IdentityOperation.IDENTITY_OPERATION_RECEIPT_ONLY,
        vertexKeys: const [],
        isLast: false,
      ),
      badIndex,
    ]) {
      final badTransport = FakeTransportBuilder()
          .server<replication.SubscribeRequest, replication.SubscribeResponse>(
            replication_spec.LanternReplicationService.subscribe,
            (_, _) => Stream.value(bad),
          )
          .build();
      final badClient = _client(badTransport);
      await expectLater(
        badClient.subscribeIdentity().toList(),
        throwsA(isA<LanternInternalException>()),
      );
      await badClient.close();
    }
  });

  test('bootstrap requires exactly one first checkpoint', () async {
    for (final frames in [
      <replication.SubscribeResponse>[],
      [_chunk()],
      [
        replication.SubscribeResponse(
          checkpoint: replication.IdentityCheckpoint(
            lastSeqPerOrigin: [MapEntry(_origin.toUpperCase(), Int64.ONE)],
          ),
        ),
      ],
      [
        replication.SubscribeResponse(
          checkpoint: replication.IdentityCheckpoint(),
        ),
        replication.SubscribeResponse(
          checkpoint: replication.IdentityCheckpoint(),
        ),
      ],
    ]) {
      final transport = FakeTransportBuilder()
          .server<replication.SubscribeRequest, replication.SubscribeResponse>(
            replication_spec.LanternReplicationService.subscribe,
            (_, _) => Stream.fromIterable(frames),
          )
          .build();
      final client = _client(transport);
      await expectLater(
        client.subscribeIdentity(bootstrap: true).toList(),
        throwsA(isA<LanternInternalException>()),
      );
      await client.close();
    }
  });

  test(
    'unexpected clean EOF requires recovery after checkpoint or chunk',
    () async {
      for (final bootstrap in [false, true]) {
        final frames = bootstrap
            ? [
                replication.SubscribeResponse(
                  checkpoint: replication.IdentityCheckpoint(),
                ),
              ]
            : [_chunk()];
        final transport = FakeTransportBuilder()
            .server<
              replication.SubscribeRequest,
              replication.SubscribeResponse
            >(
              replication_spec.LanternReplicationService.subscribe,
              (_, _) => Stream.fromIterable(frames),
            )
            .build();
        final client = _client(transport);
        await expectLater(
          client.subscribeIdentity(bootstrap: bootstrap).toList(),
          throwsA(isA<LanternFailedPreconditionException>()),
        );
        await client.close();
      }
    },
  );

  test('explicit cancellation does not turn clean EOF into recovery', () async {
    final listening = Completer<void>();
    final source = StreamController<replication.SubscribeResponse>(
      onListen: listening.complete,
    );
    final cancellation = LanternCancellationToken();
    final client = _client(_DirectIdentityStreamTransport(source.stream));
    addTearDown(client.close);
    final errors = <Object>[];
    final done = Completer<void>();
    client
        .subscribeIdentity(
          bootstrap: true,
          options: LanternCallOptions(cancellation: cancellation),
        )
        .listen((_) {}, onError: errors.add, onDone: done.complete);
    await listening.future.timeout(const Duration(seconds: 2));
    cancellation.cancel();
    await source.close();
    await done.future.timeout(const Duration(seconds: 2));
    expect(errors, isEmpty);
  });

  test('stream maps auth failure and cancellation without retry', () async {
    var calls = 0;
    final transport = FakeTransportBuilder()
        .server<replication.SubscribeRequest, replication.SubscribeResponse>(
          replication_spec.LanternReplicationService.subscribe,
          (_, _) {
            calls++;
            throw connect.ConnectException(
              connect.Code.unauthenticated,
              'credentials rejected',
            );
          },
        )
        .build();
    final client = _client(transport);
    addTearDown(client.close);
    await expectLater(
      client.subscribeIdentity().toList(),
      throwsA(isA<LanternUnauthenticatedException>()),
    );
    expect(calls, 1);

    final cancellation = LanternCancellationToken();
    final source = StreamController<replication.SubscribeResponse>();
    final canceled = Completer<void>();
    final streamingTransport = FakeTransportBuilder()
        .server<replication.SubscribeRequest, replication.SubscribeResponse>(
          replication_spec.LanternReplicationService.subscribe,
          (_, context) {
            context.signal.future.then((_) => canceled.complete());
            return source.stream;
          },
        )
        .build();
    final streamingClient = _client(streamingTransport);
    addTearDown(streamingClient.close);
    final subscription = streamingClient
        .subscribeIdentity(
          options: LanternCallOptions(cancellation: cancellation),
        )
        .listen((_) {});
    await Future<void>.delayed(Duration.zero);
    cancellation.cancel();
    await canceled.future.timeout(const Duration(seconds: 2));
    await subscription.cancel();
    await source.close();
  });
}
