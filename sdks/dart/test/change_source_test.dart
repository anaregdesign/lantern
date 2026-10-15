import 'dart:async';

import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/changes.connect.spec.dart'
    as changes;
import 'package:lantern_client/src/gen/graph/v1/changes.pb.dart' as wire;
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart'
    as graphspec;
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

final endpoint = Uri.parse('https://lantern.test');
LanternSessionBinding binding([String authority = 'verified-cut']) =>
    LanternSessionBinding.application(
      serviceOrigin: endpoint,
      securityDomain: 'fixture',
      accountIdentity: 'account',
      authorityBinding: authority,
    );

void main() {
  test('full binding, route and identity-only scope are required', () async {
    final transport = FakeTransportBuilder()
        .server<wire.WatchChangesRequest, wire.WatchChangesResponse>(
          changes.LanternChangeService.watchChanges,
          (request, context) async* {
            expect(request.prefix, 'orders:');
            expect(
              request.projection,
              wire.ChangeProjection.CHANGE_PROJECTION_IDENTITY,
            );
            yield wire.WatchChangesResponse(bootstrap: true, cursor: [1]);
            yield wire.WatchChangesResponse(
              cursor: [2],
              invalidations: [wire.ChangeInvalidation(vertexKey: 'private:1')],
            );
            await context.signal.future;
          },
        )
        .build();
    final client = LanternClient.connect(endpoint, transport: transport);
    addTearDown(client.close);
    var current = binding(), affinity = true;
    final route = LanternSingleResponderRoute.certified(
      client: client,
      responderId: 'direct-node',
      isCurrent: () => affinity,
    );
    final source = LanternChangeSource(
      route: route,
      binding: current,
      currentBinding: () => current,
      prefix: 'orders:',
    );
    final session = source.open(
      bootstrap: true,
      cancellation: LanternCancellationToken(),
    );
    addTearDown(session.close);
    final frames = <ChangeFrame>[];
    await expectLater(
      session.frames.forEach(frames.add),
      throwsA(isA<LanternChangeDiscontinuity>()),
    );
    expect(frames.single.cursor!.toBytes(), [1]);
    current = binding('different-cut');
    expect(source.checkCurrent, throwsA(isA<LanternChangeDiscontinuity>()));
    current = binding();
    affinity = false;
    expect(source.checkCurrent, throwsA(isA<LanternChangeDiscontinuity>()));
    expect(
      () => LanternChangeSource(
        route: route,
        binding: LanternSessionBinding.application(
          serviceOrigin: Uri.parse('https://other.test'),
          securityDomain: 'fixture',
          accountIdentity: 'account',
          authorityBinding: 'verified-cut',
        ),
        currentBinding: binding,
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
  });

  test(
    'authority changes during token acquisition prevent stream dispatch',
    () async {
      final token = Completer<String>();
      var calls = 0, current = binding();
      final client = LanternClient.connect(
        endpoint,
        tokenProvider: () => token.future,
        transport: FakeTransportBuilder()
            .server<wire.WatchChangesRequest, wire.WatchChangesResponse>(
              changes.LanternChangeService.watchChanges,
              (request, context) async* {
                calls++;
                yield wire.WatchChangesResponse(bootstrap: true, cursor: [1]);
              },
            )
            .build(),
      );
      addTearDown(client.close);
      final source = LanternChangeSource(
        route: LanternSingleResponderRoute.certified(
          client: client,
          responderId: 'node',
          isCurrent: () => true,
        ),
        binding: current,
        currentBinding: () => current,
      );
      final session = source.open(
        bootstrap: true,
        cancellation: LanternCancellationToken(),
      );
      addTearDown(session.close);
      final failed = expectLater(
        session.frames.first,
        throwsA(isA<LanternChangeDiscontinuity>()),
      );
      await Future<void>.delayed(Duration.zero);
      current = binding('new-authority');
      token.complete('fixture-credential');
      await failed;
      expect(calls, 0);
    },
  );

  test(
    'read publication is fenced; cancel joins without closing transport',
    () async {
      final held = Completer<graph.GetVerticesResponse>(),
          dispatched = Completer<void>();
      var affinity = true;
      final client = LanternClient.connect(
        endpoint,
        transport: FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              graphspec.LanternService.getVertices,
              (request, _) {
                dispatched.complete();
                expect(request.keys, ['one', 'one']);
                return held.future;
              },
            )
            .build(),
      );
      addTearDown(client.close);
      final source = LanternChangeSource(
        route: LanternSingleResponderRoute.certified(
          client: client,
          responderId: 'node',
          isCurrent: () => affinity,
        ),
        binding: binding(),
        currentBinding: binding,
      );
      final session = source.open(
        bootstrap: true,
        cancellation: LanternCancellationToken(),
      );
      final read = session.getVertices(['one', 'one']);
      final rejected = expectLater(
        read,
        throwsA(isA<LanternChangeDiscontinuity>()),
      );
      await dispatched.future;
      affinity = false;
      held.complete(
        graph.GetVerticesResponse(
          vertices: [graph.Vertex(key: 'one', string: 'late')],
        ),
      );
      await rejected;
      await session.close();
      expect(client.isClosed, isFalse);
    },
  );

  test('caller cancellation remains typed at bridge read boundaries', () async {
    final client = LanternClient.connect(
      endpoint,
      transport: FakeTransportBuilder().build(),
    );
    addTearDown(client.close);
    final source = LanternChangeSource(
      route: LanternSingleResponderRoute.certified(
        client: client,
        responderId: 'node',
        isCurrent: () => true,
      ),
      binding: binding(),
      currentBinding: binding,
    );
    final session = source.open(
      bootstrap: true,
      cancellation: LanternCancellationToken(),
    );
    addTearDown(session.close);
    final token = LanternCancellationToken()..cancel();
    await expectLater(
      session.getVertices(['one'], cancellation: token),
      throwsA(isA<LanternCanceledException>()),
    );
  });
}
