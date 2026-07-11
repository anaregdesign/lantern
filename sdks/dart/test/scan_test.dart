import 'dart:async';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test(
    'page cursors are defensive and order/cursor pass through exactly',
    () async {
      late graph.ScanVerticesRequest captured;
      final responseCursor = Uint8List.fromList([1, 2, 3]);
      final transport = FakeTransportBuilder()
          .unary<graph.ScanVerticesRequest, graph.ScanVerticesResponse>(
            LanternService.scanVertices,
            (request, context) {
              captured = request.clone();
              return graph.ScanVerticesResponse(
                vertices: [graph.Vertex(key: 'v', string: 'value')],
                nextCursor: responseCursor,
              );
            },
          )
          .build();
      final client = _client(transport);
      final inputBytes = Uint8List.fromList([9, 8]);
      final cursor = ScanCursor.fromBytes(inputBytes);
      inputBytes[0] = 0;

      final page = await client.scanVertices(
        prefix: 'p:',
        limit: 7,
        cursor: cursor,
        order: ScanOrder.descending,
      );

      expect(captured.prefix, 'p:');
      expect(captured.limit, 7);
      expect(captured.cursor, [9, 8]);
      expect(captured.order, graph.ScanOrder.SCAN_ORDER_DESC);
      expect(page.items.single.key, 'v');
      expect(page.hasMore, isTrue);
      responseCursor[0] = 0;
      final exported = page.nextCursor!.toBytes();
      expect(exported, [1, 2, 3]);
      exported[0] = 0;
      expect(page.nextCursor!.toBytes(), [1, 2, 3]);
      expect(() => page.items.add(page.items.single), throwsUnsupportedError);
    },
  );

  test('keys-only and edge pages use their own wire requests', () async {
    late graph.ScanVertexKeysRequest keyRequest;
    late graph.ScanEdgesRequest edgeRequest;
    final transport = FakeTransportBuilder()
        .unary<graph.ScanVertexKeysRequest, graph.ScanVertexKeysResponse>(
          LanternService.scanVertexKeys,
          (request, context) {
            keyRequest = request.clone();
            return graph.ScanVertexKeysResponse(keys: ['p:a', 'p:b']);
          },
        )
        .unary<graph.ScanEdgesRequest, graph.ScanEdgesResponse>(
          LanternService.scanEdges,
          (request, context) {
            edgeRequest = request.clone();
            return graph.ScanEdgesResponse(
              edges: [graph.Edge(tail: 't:a', head: 'h:b', weight: 2)],
            );
          },
        )
        .build();
    final client = _client(transport);

    final keys = await client.scanVertexKeys(
      prefix: 'p:',
      order: ScanOrder.ascending,
    );
    final edges = await client.scanEdges(tailPrefix: 't:', headPrefix: 'h:');

    expect(keys.items, ['p:a', 'p:b']);
    expect(keyRequest.order, graph.ScanOrder.SCAN_ORDER_ASC);
    expect(edges.items.single.weight, 2);
    expect(edgeRequest.tailPrefix, 't:');
    expect(edgeRequest.headPrefix, 'h:');
    expect(keys.hasMore, isFalse);
    expect(edges.hasMore, isFalse);
  });

  test('all-pages stream honors pause, resume, and cancellation', () async {
    var calls = 0;
    var active = 0;
    var maxActive = 0;
    var activeCanceled = false;
    final secondStarted = Completer<void>();
    final transport = FakeTransportBuilder()
        .unary<graph.ScanVertexKeysRequest, graph.ScanVertexKeysResponse>(
          LanternService.scanVertexKeys,
          (request, context) async {
            calls++;
            active++;
            if (active > maxActive) maxActive = active;
            if (calls == 1) {
              active--;
              return graph.ScanVertexKeysResponse(
                keys: ['p:a'],
                nextCursor: [1],
              );
            }
            secondStarted.complete();
            await context.signal.future;
            activeCanceled = true;
            active--;
            throw connect.ConnectException(
              connect.Code.canceled,
              'expected cancellation',
            );
          },
        )
        .build();
    final client = _client(transport);
    late StreamSubscription<Page<String>> subscription;
    final firstPage = Completer<void>();
    subscription = client.scanVertexKeysAll(prefix: 'p:', limit: 1).listen((
      page,
    ) {
      subscription.pause();
      firstPage.complete();
    });

    await firstPage.future;
    await Future<void>.delayed(const Duration(milliseconds: 10));
    expect(calls, 1);
    subscription.resume();
    await secondStarted.future;
    expect(calls, 2);
    expect(maxActive, 1);
    await subscription.cancel();
    await Future<void>.delayed(Duration.zero);
    expect(activeCanceled, isTrue);
    expect(calls, 2);
  });

  test('caller cancellation wakes a stream paused between pages', () async {
    var calls = 0;
    final cancellation = LanternCancellationToken();
    final firstPage = Completer<void>();
    final canceled = Completer<Object>();
    final transport = FakeTransportBuilder()
        .unary<graph.ScanVertexKeysRequest, graph.ScanVertexKeysResponse>(
          LanternService.scanVertexKeys,
          (request, context) {
            calls++;
            return graph.ScanVertexKeysResponse(keys: ['p:a'], nextCursor: [1]);
          },
        )
        .build();
    final client = _client(transport);
    late StreamSubscription<Page<String>> subscription;
    subscription = client
        .scanVertexKeysAll(
          prefix: 'p:',
          options: LanternCallOptions(cancellation: cancellation),
        )
        .listen((page) {
          subscription.pause();
          firstPage.complete();
        }, onError: (Object error) => canceled.complete(error));

    await firstPage.future;
    cancellation.cancel('screen disposed');
    subscription.resume();

    expect(await canceled.future, isA<LanternCanceledException>());
    expect(calls, 1);
    await subscription.cancel();
  });

  test(
    'count and scoped prefix deletes preserve exact uint64 values',
    () async {
      final maxUint64 = Int64.fromInts(0xffffffff, 0xffffffff);
      late graph.DeleteVerticesByPrefixRequest vertexDelete;
      late graph.DeleteEdgesByPrefixRequest edgeDelete;
      final transport = FakeTransportBuilder()
          .unary<
            graph.CountVerticesByPrefixRequest,
            graph.CountVerticesByPrefixResponse
          >(
            LanternService.countVerticesByPrefix,
            (request, context) =>
                graph.CountVerticesByPrefixResponse(count: maxUint64),
          )
          .unary<
            graph.DeleteVerticesByPrefixRequest,
            graph.DeleteVerticesByPrefixResponse
          >(LanternService.deleteVerticesByPrefix, (request, context) {
            vertexDelete = request.clone();
            return graph.DeleteVerticesByPrefixResponse(deleted: Int64(2));
          })
          .unary<
            graph.DeleteEdgesByPrefixRequest,
            graph.DeleteEdgesByPrefixResponse
          >(LanternService.deleteEdgesByPrefix, (request, context) {
            edgeDelete = request.clone();
            return graph.DeleteEdgesByPrefixResponse(deleted: Int64(3));
          })
          .build();
      final client = _client(transport);

      expect(
        await client.countVerticesByPrefix(''),
        (BigInt.one << 64) - BigInt.one,
      );
      expect(
        await client.deleteVerticesByPrefix('p:', limit: 2, dryRun: true),
        BigInt.two,
      );
      expect(vertexDelete.prefix, 'p:');
      expect(vertexDelete.limit, 2);
      expect(vertexDelete.dryRun, isTrue);
      expect(
        await client.deleteEdgesByPrefix(headPrefix: 'h:', limit: 3),
        BigInt.from(3),
      );
      expect(edgeDelete.tailPrefix, isEmpty);
      expect(edgeDelete.headPrefix, 'h:');
      expect(edgeDelete.limit, 3);
    },
  );

  test('unsafe prefix deletes validate locally and never retry', () async {
    var vertexCalls = 0;
    var edgeCalls = 0;
    final transport = FakeTransportBuilder()
        .unary<
          graph.DeleteVerticesByPrefixRequest,
          graph.DeleteVerticesByPrefixResponse
        >(LanternService.deleteVerticesByPrefix, (request, context) {
          vertexCalls++;
          throw connect.ConnectException(
            connect.Code.unavailable,
            'lost committed response',
          );
        })
        .unary<
          graph.DeleteEdgesByPrefixRequest,
          graph.DeleteEdgesByPrefixResponse
        >(LanternService.deleteEdgesByPrefix, (request, context) {
          edgeCalls++;
          throw connect.ConnectException(
            connect.Code.unavailable,
            'lost committed response',
          );
        })
        .build();
    final client = _client(transport, retry: const RetryPolicy());

    await expectLater(
      client.scanVertexKeys(prefix: ''),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    await expectLater(
      client.deleteVerticesByPrefix(''),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    await expectLater(
      client.deleteEdgesByPrefix(),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    await expectLater(
      client.scanEdges(limit: -1),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    await expectLater(
      client.deleteVerticesByPrefix('p:', limit: 1),
      throwsA(isA<LanternUnavailableException>()),
    );
    await expectLater(
      client.deleteEdgesByPrefix(tailPrefix: 't:', limit: 1),
      throwsA(isA<LanternUnavailableException>()),
    );
    expect(vertexCalls, 1);
    expect(edgeCalls, 1);
  });
}

LanternClient _client(connect.Transport transport, {RetryPolicy? retry}) =>
    LanternClient.connect(
      Uri.parse('https://example.test'),
      transport: transport,
      retryPolicy: retry,
    );
