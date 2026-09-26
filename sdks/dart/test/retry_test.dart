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

  test(
    'plain singular Add never retries with absent, minted, or caller IDs',
    () async {
      final suppliedId = Uint8List(24)..[23] = 1;
      final scenarios = <({String name, bool idempotentAdds, Uint8List? id})>[
        (name: 'absent', idempotentAdds: false, id: null),
        (name: 'minted', idempotentAdds: true, id: null),
        (name: 'caller', idempotentAdds: false, id: suppliedId),
      ];
      for (final scenario in scenarios) {
        var calls = 0;
        late graph.AddEdgesRequest captured;
        final transport = FakeTransportBuilder()
            .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
              LanternService.addEdges,
              (request, context) {
                calls++;
                captured = request.deepCopy();
                throw connect.ConnectException(
                  connect.Code.unavailable,
                  'response lost',
                );
              },
            )
            .build();
        final client = _client(
          transport,
          retryPolicy: _fastRetry,
          idempotentAdds: scenario.idempotentAdds,
        );

        await expectLater(
          client.addEdge(
            EdgeInput(tail: 'a', head: 'b', weight: 1, contribId: scenario.id),
          ),
          throwsA(isA<LanternUnavailableException>()),
        );
        expect(calls, 1, reason: scenario.name);
        if (scenario.id != null) {
          expect(captured.contribIds.single, suppliedId, reason: scenario.name);
        } else if (scenario.idempotentAdds) {
          expect(
            captured.contribIds.single,
            hasLength(24),
            reason: scenario.name,
          );
        } else {
          expect(captured.contribIds, isEmpty, reason: scenario.name);
        }
      }
    },
  );

  test('plural Add never retries an ambiguous chunk with mixed IDs', () async {
    var calls = 0;
    final requests = <graph.AddEdgesRequest>[];
    final transport = FakeTransportBuilder()
        .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
          LanternService.addEdges,
          (request, context) {
            calls++;
            requests.add(request.deepCopy());
            if (calls == 1) {
              return graph.AddEdgesResponse(
                written: 2,
                effectiveWeights: [1, 2],
              );
            }
            throw connect.ConnectException(
              connect.Code.unavailable,
              'response lost',
            );
          },
        )
        .build();
    final suppliedId = Uint8List(24)..[23] = 2;
    final client = _client(
      transport,
      retryPolicy: _fastRetry,
      idempotentAdds: true,
    );

    await expectLater(
      client.addEdges([
        EdgeInput(tail: 'a', head: 'b', weight: 1, contribId: suppliedId),
        EdgeInput(tail: 'b', head: 'c', weight: 2),
        EdgeInput(tail: 'c', head: 'd', weight: 3),
      ], batchSize: 2),
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
    expect(calls, 2);
    expect(requests.map((request) => request.edges.length), [2, 1]);
    expect(requests[0].contribIds[0], suppliedId);
    final firstGenerated = requests[0].contribIds[1];
    final secondGenerated = requests[1].contribIds.single;
    expect(firstGenerated, hasLength(24));
    expect(secondGenerated, hasLength(24));
    expect(firstGenerated.sublist(0, 22), secondGenerated.sublist(0, 22));
    expect(firstGenerated.sublist(22), [0, 1]);
    expect(secondGenerated.sublist(22), [0, 2]);
  });

  test('decaying Add with minted IDs never retries a lost response', () async {
    var calls = 0;
    late graph.AddEdgesRequest captured;
    final transport = FakeTransportBuilder()
        .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
          LanternService.addEdges,
          (request, context) {
            calls++;
            captured = request.deepCopy();
            throw connect.ConnectException(
              connect.Code.unavailable,
              'response lost',
            );
          },
        )
        .build();
    final client = _client(
      transport,
      retryPolicy: _fastRetry,
      idempotentAdds: true,
    );

    await expectLater(
      client.addDecayingEdge(
        tail: 'a',
        head: 'b',
        options: const DecayOptions(
          initialWeight: 8,
          ratio: 0.5,
          steps: 3,
          interval: Duration(minutes: 1),
        ),
      ),
      throwsA(isA<LanternUnavailableException>()),
    );
    expect(calls, 1);
    expect(captured.edges, hasLength(3));
    expect(captured.contribIds, hasLength(3));
    expect(captured.contribIds.every((id) => id.length == 24), isTrue);
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
    for (final method in ['AddEdge', 'AddEdges', 'AddDecayingEdge']) {
      expect(
        RetryRegistry.classify(method),
        RpcRetryClass.never,
        reason: method,
      );
    }
    expect(RetryRegistry.classify('GetEdge'), RpcRetryClass.read);
    expect(RetryRegistry.classify('PutEdges'), RpcRetryClass.stablePut);
    expect(
      RetryRegistry.classify('AddEdgesWithReceipt'),
      RpcRetryClass.receiptMutation,
    );
    expect(
      RetryRegistry.classify('DeleteEdgesWithReceipt'),
      RpcRetryClass.receiptMutation,
    );
    expect(
      RetryRegistry.classify('PutVerticesWithReceipt'),
      RpcRetryClass.receiptMutation,
    );
    expect(
      RetryRegistry.classify('DeleteVerticesWithReceipt'),
      RpcRetryClass.receiptMutation,
    );
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
