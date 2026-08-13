import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/client.dart'
    show RetryRegistry, RpcRetryClass;
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test('contribution IDs match the cross-SDK golden vectors', () {
    final nonce = Uint8List.fromList(List.generate(16, (index) => index));
    expect(contributionIdFrom(nonce: nonce, sequence: BigInt.one, index: 0), [
      ...nonce,
      0x00,
      0x00,
      0x00,
      0x00,
      0x00,
      0x01,
      0x00,
      0x00,
    ]);
    expect(
      contributionIdFrom(
        nonce: nonce,
        sequence: BigInt.from(0xabcd),
        index: 0xffff,
      ).sublist(16),
      [0x00, 0x00, 0x00, 0x00, 0xab, 0xcd, 0xff, 0xff],
    );
  });

  test('read retries unavailable and exhaustion remains typed', () async {
    var calls = 0;
    final transport = FakeTransportBuilder()
        .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
          LanternService.getVertices,
          (request, context) {
            calls++;
            throw connect.ConnectException(connect.Code.unavailable, 'down');
          },
        )
        .build();
    final client = _client(
      transport,
      retryPolicy: const RetryPolicy(
        maxAttempts: 3,
        baseDelay: Duration(microseconds: 1),
        maxDelay: Duration(microseconds: 1),
      ),
    );

    await expectLater(
      client.getVertex('key'),
      throwsA(
        isA<LanternRetryExhaustedException>()
            .having((error) => error.attempts, 'attempts', 3)
            .having(
              (error) => error.cause,
              'cause',
              isA<LanternUnavailableException>(),
            ),
      ),
    );
    expect(calls, 3);
  });

  test('single-RPC suppression performs exactly one wire attempt', () async {
    var calls = 0;
    final transport = FakeTransportBuilder()
        .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
          LanternService.getVertices,
          (request, context) {
            calls++;
            throw connect.ConnectException(connect.Code.unavailable, 'down');
          },
        )
        .build();
    final client = _client(
      transport,
      retryPolicy: _fastRetry,
      defaultTimeout: null,
    );

    await expectLater(
      client.getVertex('key', options: LanternCallOptions(retry: false)),
      throwsA(isA<LanternUnavailableException>()),
    );
    expect(calls, 1);
  });

  test('retry suppression applies per RPC across a chunked call', () async {
    final callsByChunk = <String, int>{};
    final transport = FakeTransportBuilder()
        .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
          LanternService.putVertices,
          (request, context) {
            final chunk = request.vertices
                .map((vertex) => vertex.key)
                .join(',');
            callsByChunk[chunk] = (callsByChunk[chunk] ?? 0) + 1;
            if (request.vertices.length == 1 &&
                request.vertices.first.key == 'third') {
              throw connect.ConnectException(
                connect.Code.unavailable,
                'second chunk unavailable',
              );
            }
            return graph.PutVerticesResponse(
              outcomes: List<graph.PutOutcome>.filled(
                request.vertices.length,
                graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE,
              ),
            );
          },
        )
        .build();
    final client = _client(
      transport,
      retryPolicy: _fastRetry,
      defaultTimeout: null,
    );

    await expectLater(
      client.putVertices(
        <VertexInput>[
          VertexInput(key: 'first', value: VertexValue.nil()),
          VertexInput(key: 'second', value: VertexValue.nil()),
          VertexInput(key: 'third', value: VertexValue.nil()),
        ],
        batchSize: 2,
        options: LanternCallOptions(retry: false),
      ),
      throwsA(
        isA<BatchException>()
            .having((error) => error.committed, 'committed', 2)
            .having(
              (error) => error.cause,
              'cause',
              isA<LanternUnavailableException>(),
            ),
      ),
    );
    expect(callsByChunk, <String, int>{'first,second': 1, 'third': 1});
  });

  test('scan stream preserves per-call retry suppression', () async {
    var calls = 0;
    final transport = FakeTransportBuilder()
        .unary<graph.ScanVertexKeysRequest, graph.ScanVertexKeysResponse>(
          LanternService.scanVertexKeys,
          (request, context) {
            calls++;
            throw connect.ConnectException(connect.Code.unavailable, 'down');
          },
        )
        .build();
    final client = _client(transport, retryPolicy: _fastRetry);

    await expectLater(
      client
          .scanVertexKeysAll(
            prefix: 'key:',
            options: LanternCallOptions(retry: false),
          )
          .toList(),
      throwsA(isA<LanternUnavailableException>()),
    );
    expect(calls, 1);
  });

  test('Add retries only with IDs and reuses bytes across attempts', () async {
    var unsafeCalls = 0;
    final unsafeTransport = FakeTransportBuilder()
        .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
          LanternService.addEdges,
          (request, context) {
            unsafeCalls++;
            throw connect.ConnectException(connect.Code.unavailable, 'lost');
          },
        )
        .build();
    final unsafe = _client(unsafeTransport, retryPolicy: _fastRetry);
    await expectLater(
      unsafe.addEdge(EdgeInput(tail: 'a', head: 'b', weight: 1)),
      throwsA(isA<LanternUnavailableException>()),
    );
    expect(unsafeCalls, 1);

    var safeCalls = 0;
    final seen = <List<List<int>>>[];
    final safeTransport = FakeTransportBuilder()
        .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
          LanternService.addEdges,
          (request, context) {
            safeCalls++;
            seen.add(
              request.contribIds.map((value) => List<int>.from(value)).toList(),
            );
            if (safeCalls == 1) {
              throw connect.ConnectException(
                connect.Code.unavailable,
                'response lost',
              );
            }
            return graph.AddEdgesResponse(written: 2, effectiveWeights: [1, 2]);
          },
        )
        .build();
    final safe = _client(
      safeTransport,
      retryPolicy: _fastRetry,
      idempotentAdds: true,
    );
    final result = await safe.addEdges([
      EdgeInput(tail: 'a', head: 'b', weight: 1),
      EdgeInput(tail: 'b', head: 'c', weight: 2),
    ]);
    expect(result.effectiveWeights, [1, 2]);
    expect(safeCalls, 2);
    expect(seen[0], seen[1]);
    expect(seen, hasLength(2));
    expect(seen[0], hasLength(2));
    expect(seen[0][0], hasLength(24));
    expect(seen[0][0].sublist(0, 16), seen[0][1].sublist(0, 16));
    expect(seen[0][0].sublist(16, 22), seen[0][1].sublist(16, 22));
    expect(seen[0][0].sublist(22), [0, 0]);
    expect(seen[0][1].sublist(22), [0, 1]);
  });

  test('stable Put replays one request and one absolute expiration', () async {
    var calls = 0;
    final expirations = <DateTime>[];
    final transport = FakeTransportBuilder()
        .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
          LanternService.putVertices,
          (request, context) {
            calls++;
            expirations.add(request.vertices.single.expiration.toDateTime());
            if (calls == 1) {
              throw connect.ConnectException(connect.Code.unavailable, 'lost');
            }
            return graph.PutVerticesResponse(
              outcomes: [graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE],
            );
          },
        )
        .build();
    var clockCalls = 0;
    final client = _client(
      transport,
      retryPolicy: _fastRetry,
      clock: () {
        clockCalls++;
        return DateTime.parse('2026-07-12T00:00:00Z');
      },
    );

    expect(
      await client.putVertex(
        VertexInput(
          key: 'key',
          value: VertexValue.nil(),
          expiresIn: const Duration(minutes: 5),
        ),
      ),
      PutOutcome.appliedAndLive,
    );
    expect(calls, 2);
    expect(clockCalls, 2);
    expect(expirations.toSet(), {DateTime.parse('2026-07-12T00:05:00Z')});
  });

  test('PutIfAbsent and Delete preserve ambiguous result semantics', () async {
    var putCalls = 0;
    var deleteCalls = 0;
    final transport = FakeTransportBuilder()
        .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
          LanternService.putVertices,
          (request, context) {
            putCalls++;
            throw connect.ConnectException(connect.Code.unavailable, 'lost');
          },
        )
        .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
          LanternService.deleteVertices,
          (request, context) {
            deleteCalls++;
            throw connect.ConnectException(connect.Code.unavailable, 'lost');
          },
        )
        .build();
    final client = _client(transport, retryPolicy: _fastRetry);

    await expectLater(
      client.putVertexIfAbsent(
        VertexInput(key: 'key', value: VertexValue.nil()),
      ),
      throwsA(isA<LanternUnavailableException>()),
    );
    await expectLater(
      client.deleteVertex('key'),
      throwsA(isA<LanternUnavailableException>()),
    );
    expect(putCalls, 1);
    expect(deleteCalls, 1);
  });

  test('resource exhausted requires explicit opt-in', () async {
    Future<void> run({required bool optIn, required int expectedCalls}) async {
      var calls = 0;
      final transport = FakeTransportBuilder()
          .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
            LanternService.getVertices,
            (request, context) {
              calls++;
              if (calls == 1) {
                throw connect.ConnectException(
                  connect.Code.resourceExhausted,
                  'busy',
                );
              }
              return graph.GetVerticesResponse(
                vertices: [graph.Vertex(key: 'key', nil: true)],
              );
            },
          )
          .build();
      final client = _client(
        transport,
        retryPolicy: RetryPolicy(
          maxAttempts: 2,
          baseDelay: const Duration(microseconds: 1),
          maxDelay: const Duration(microseconds: 1),
          retryResourceExhausted: optIn,
        ),
      );
      if (optIn) {
        expect((await client.getVertex('key')).key, 'key');
      } else {
        await expectLater(
          client.getVertex('key'),
          throwsA(isA<LanternResourceExhaustedException>()),
        );
      }
      expect(calls, expectedCalls);
    }

    await run(optIn: false, expectedCalls: 1);
    await run(optIn: true, expectedCalls: 2);
  });

  test('cancellation and overall deadline stop backoff immediately', () async {
    final firstAttempt = Completer<void>();
    final transport = FakeTransportBuilder()
        .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
          LanternService.getVertices,
          (request, context) {
            if (!firstAttempt.isCompleted) firstAttempt.complete();
            throw connect.ConnectException(connect.Code.unavailable, 'down');
          },
        )
        .build();
    final cancellation = LanternCancellationToken();
    final client = _client(
      transport,
      retryPolicy: const RetryPolicy(
        maxAttempts: 3,
        baseDelay: Duration(seconds: 2),
        maxDelay: Duration(seconds: 2),
      ),
    );
    final call = client.getVertex(
      'key',
      options: LanternCallOptions(cancellation: cancellation),
    );
    await firstAttempt.future;
    cancellation.cancel();
    await expectLater(call, throwsA(isA<LanternCanceledException>()));

    await expectLater(
      client.getVertex(
        'key',
        options: LanternCallOptions(timeout: const Duration(milliseconds: 10)),
      ),
      throwsA(isA<LanternDeadlineExceededException>()),
    );
  });

  test('retry registry covers every generated RPC and fails closed', () {
    final source = File(
      'lib/src/gen/graph/v1/graph.connect.spec.dart',
    ).readAsStringSync();
    final generated = RegExp(
      r"'/\$name/([A-Za-z]+)'",
    ).allMatches(source).map((match) => match.group(1)!).toSet();

    expect(RetryRegistry.classifications.keys.toSet(), containsAll(generated));
    expect(RetryRegistry.classify('FutureUnknownMethod'), RpcRetryClass.never);
    expect(RetryRegistry.classify('DeleteVertex'), RpcRetryClass.never);
    expect(RetryRegistry.classify('BackupSnapshot'), RpcRetryClass.stream);
    expect(RetryRegistry.classify('SearchVerticesPage'), RpcRetryClass.read);
  });
}

const _fastRetry = RetryPolicy(
  maxAttempts: 3,
  baseDelay: Duration(microseconds: 1),
  maxDelay: Duration(microseconds: 1),
);

LanternClient _client(
  connect.Transport transport, {
  RetryPolicy? retryPolicy,
  bool idempotentAdds = false,
  LanternClock? clock,
  Duration? defaultTimeout = const Duration(seconds: 10),
}) => LanternClient.connect(
  Uri.parse('https://example.test'),
  transport: transport,
  retryPolicy: retryPolicy,
  idempotentAdds: idempotentAdds,
  clock: clock,
  defaultTimeout: defaultTimeout,
);
