import 'dart:async';
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/changes.connect.spec.dart'
    as spec;
import 'package:lantern_client/src/gen/graph/v1/changes.pb.dart' as wire;
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test(
    'opaque cursor and checked identity stream have one cancellable attempt',
    () async {
      final bytes = [1, 2, 3];
      final cursor = ChangeCursor(bytes);
      bytes[0] = 9;
      final exported = cursor.toBytes();
      exported[0] = 8;
      expect(cursor.toBytes(), [1, 2, 3]);
      var opened = 0;
      final canceled = Completer<void>();
      final transport = FakeTransportBuilder()
          .server<wire.WatchChangesRequest, wire.WatchChangesResponse>(
            spec.LanternChangeService.watchChanges,
            (input, context) async* {
              opened++;
              expect(context.signal.deadline, isNull);
              expect(input.cursor, [1, 2, 3]);
              expect(input.prefix, 'orders:');
              yield wire.WatchChangesResponse(
                cursor: [4],
                invalidations: [wire.ChangeInvalidation(vertexKey: 'orders:1')],
              );
              await context.signal.future;
              canceled.complete();
            },
          )
          .build();
      final client = LanternClient.connect(
        Uri.parse('https://lantern.test'),
        transport: transport,
        defaultTimeout: const Duration(microseconds: 1),
      );
      addTearDown(client.close);
      final frame = await client
          .watchChanges(prefix: 'orders:', cursor: cursor)
          .first;
      expect(
        (frame.invalidations.single as VertexInvalidation).key,
        'orders:1',
      );
      expect(
        (frame.invalidations.single as VertexInvalidation).current,
        isNull,
      );
      expect(() => frame.invalidations.clear(), throwsUnsupportedError);
      await canceled.future.timeout(const Duration(seconds: 2));
      expect(opened, 1);
    },
  );
  test(
    'value, unknown-field and bootstrap violations fail without another attempt',
    () async {
      final good = wire.WatchChangesResponse(
        cursor: [1],
        invalidations: [
          wire.ChangeInvalidation(
            vertexKey: 'orders:1',
            vertex: graph.Vertex(key: 'orders:1', string: 'current'),
          ),
        ],
      );
      final badImage = wire.WatchChangesResponse(
        cursor: [1],
        invalidations: [
          wire.ChangeInvalidation(
            vertexKey: 'orders:1',
            vertex: graph.Vertex(key: 'orders:2'),
          ),
        ],
      );
      final unknown = wire.WatchChangesResponse.fromBuffer([
        ...good.writeToBuffer(),
        0x98,
        0x06,
        1,
      ]);
      for (final item in [
        (good, ChangeProjection.identity, false),
        (badImage, ChangeProjection.value, false),
        (unknown, ChangeProjection.value, false),
        (good, ChangeProjection.value, true),
      ]) {
        final transport = FakeTransportBuilder()
            .server<wire.WatchChangesRequest, wire.WatchChangesResponse>(
              spec.LanternChangeService.watchChanges,
              (input, context) async* {
                yield item.$1;
              },
            )
            .build();
        final client = LanternClient.connect(
          Uri.parse('https://lantern.test'),
          transport: transport,
        );
        addTearDown(client.close);
        await expectLater(
          client
              .watchChanges(
                bootstrap: item.$3,
                cursor: item.$3 ? null : ChangeCursor([1]),
                projection: item.$2,
              )
              .first,
          throwsA(isA<LanternInternalException>()),
        );
      }
    },
  );
  test(
    'clean EOF is a gap; explicit caller timeout and token remain in effect',
    () async {
      final transport = FakeTransportBuilder()
          .server<wire.WatchChangesRequest, wire.WatchChangesResponse>(
            spec.LanternChangeService.watchChanges,
            (input, context) async* {
              expect(context.signal.deadline, isNotNull);
              expect(
                context.requestHeaders.get('authorization')?.single,
                'Bearer named-machine',
              );
              yield wire.WatchChangesResponse(bootstrap: true, cursor: [1]);
            },
          )
          .build();
      final client = LanternClient.connect(
        Uri.parse('https://lantern.test'),
        transport: transport,
        tokenProvider: () => 'named-machine',
      );
      addTearDown(client.close);
      final frames = <ChangeFrame>[];
      await expectLater(
        client
            .watchChanges(
              bootstrap: true,
              options: LanternCallOptions(timeout: const Duration(seconds: 3)),
            )
            .forEach(frames.add),
        throwsA(isA<LanternFailedPreconditionException>()),
      );
      expect(frames.single.bootstrap, isTrue);
      expect(
        () => client.watchChanges(bootstrap: true, cursor: ChangeCursor([1])),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      expect(
        () => ChangeCursor([256]),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    },
  );
}
