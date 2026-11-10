import 'dart:async';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/changes.connect.spec.dart'
    as spec;
import 'package:lantern_client/src/gen/graph/v1/changes.pb.dart' as wire;
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;

import '../integration_test/support/contribution_change_witness.dart';

void main() {
  const prefix = 'physical-contribution:0123456789abcdef0123456789abcdef:';
  final edge = EdgeRef('${prefix}t', '${prefix}h');

  for (final copies in [1, 2]) {
    test('public scoped CDC accepts $copies matching invalidations', () async {
      final publish = Completer<void>();
      final canceled = Completer<void>();
      var opened = 0;
      final transport = FakeTransportBuilder()
          .server<wire.WatchChangesRequest, wire.WatchChangesResponse>(
            spec.LanternChangeService.watchChanges,
            (input, context) async* {
              opened++;
              expect(input.prefix, prefix);
              expect(input.bootstrap, isTrue);
              expect(input.cursor, isEmpty);
              expect(
                input.projection,
                wire.ChangeProjection.CHANGE_PROJECTION_IDENTITY,
              );
              expect(context.signal.deadline, isNull);
              yield wire.WatchChangesResponse(bootstrap: true, cursor: [1, 2]);
              await publish.future;
              yield wire.WatchChangesResponse(
                invalidations: [
                  wire.ChangeInvalidation(vertexKey: '${prefix}v'),
                ],
                cursor: [3],
              );
              yield wire.WatchChangesResponse(
                invalidations: [
                  wire.ChangeInvalidation(vertexKey: '${prefix}other'),
                  for (var i = 0; i < copies; i++)
                    wire.ChangeInvalidation(
                      edgeKey: graph.EdgeKey(tail: edge.tail, head: edge.head),
                    ),
                ],
                // An intermediate frame may not have a progress checkpoint.
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
      final witness = ContributionChangeWitness(
        client,
        prefix: prefix,
        edge: edge,
      );
      try {
        expect((await witness.ready).toBytes(), [1, 2]);
        publish.complete();
        final frame = await witness.invalidation;
        final matching = frame.invalidations
            .whereType<EdgeInvalidation>()
            .where((item) => item.edge == edge);
        expect(matching, hasLength(copies));
        expect(matching.every((item) => item.current == null), isTrue);
        expect(frame.cursor, isNull);
      } finally {
        await witness.close();
      }
      await canceled.future.timeout(const Duration(seconds: 2));
      expect(opened, 1);
    });
  }

  for (final bootstrapped in [false, true]) {
    test('stream refusal propagates after bootstrap=$bootstrapped', () async {
      final refuse = Completer<void>();
      final transport = FakeTransportBuilder()
          .server<wire.WatchChangesRequest, wire.WatchChangesResponse>(
            spec.LanternChangeService.watchChanges,
            (input, context) async* {
              if (bootstrapped) {
                yield wire.WatchChangesResponse(bootstrap: true, cursor: [1]);
              }
              await refuse.future;
              throw connect.ConnectException(
                connect.Code.permissionDenied,
                'test refusal',
              );
            },
          )
          .build();
      final client = LanternClient.connect(
        Uri.parse('https://lantern.test'),
        transport: transport,
      );
      addTearDown(client.close);
      final witness = ContributionChangeWitness(
        client,
        prefix: prefix,
        edge: edge,
      );
      try {
        if (bootstrapped) await witness.ready;
        refuse.complete();
        // The target awaits this after a mutation, not at the instant of error.
        await Future<void>.delayed(Duration.zero);
        if (!bootstrapped) {
          await expectLater(
            witness.ready,
            throwsA(isA<LanternPermissionDeniedException>()),
          );
        }
        await expectLater(
          witness.invalidation,
          throwsA(isA<LanternPermissionDeniedException>()),
        );
      } finally {
        await witness.close();
      }
    });
  }

  test('an unrelated scope cannot open the public stream', () async {
    var opened = 0;
    final transport = FakeTransportBuilder()
        .server<wire.WatchChangesRequest, wire.WatchChangesResponse>(
          spec.LanternChangeService.watchChanges,
          (input, context) async* {
            opened++;
            yield wire.WatchChangesResponse(bootstrap: true, cursor: [1]);
          },
        )
        .build();
    final client = LanternClient.connect(
      Uri.parse('https://lantern.test'),
      transport: transport,
    );
    addTearDown(client.close);
    expect(
      () => ContributionChangeWitness(client, prefix: 'orders:', edge: edge),
      throwsArgumentError,
    );
    expect(opened, 0);
  });
}
