import 'dart:io' as io;

import 'package:connectrpc/connect.dart' as connect;

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:lantern_example/app_data_session.dart';
import 'package:lantern_example/installation_policy.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

void main() {
  final url = io.Platform.environment['LANTERN_SCOPED_WIRE_URL'];
  test(
    'real public scoped CDC invalidates offline residents and denies peer/value access',
    () async {
      final context = io.SecurityContext(withTrustedRoots: false)
        ..setTrustedCertificates(
          io.Platform.environment['LANTERN_SCOPED_WIRE_CA']!,
        );
      LanternClient client(String token) => LanternClient.connect(
        Uri.parse(url!),
        httpClientFactory: () => io.HttpClient(context: context),
        tokenProvider: () => token,
        defaultTimeout: const Duration(seconds: 5),
      );
      final sdk = client(
        io.Platform.environment['LANTERN_SCOPED_WIRE_CREDENTIAL']!,
      );
      addTearDown(sdk.close);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: LanternClientOfflineRemote(sdk),
      );
      addTearDown(repository.dispose);
      await sdk.putVertex(
        VertexInput(key: 'orders:resident', value: VertexValue.string('old')),
      );
      await repository.readVertex('account', 'orders:resident');
      final source = LanternScopedChangeSource(source: _source(sdk));
      final cancellation = LanternCancellationToken();
      final consuming = repository.consumeScopedChanges(
        'account',
        source: source,
        cancellation: cancellation,
      );
      final canceled = expectLater(
        consuming,
        throwsA(isA<OfflineCanceledException>()),
      );
      await _wait(
        'legacy checkpoint established',
        () async => await store.transaction(
          (tx) async =>
              await tx.scopedChangeCursor('account') != null &&
              !(await tx.hasUnknownResident(
                'account',
                const OfflineEntityKey.vertex('orders:resident'),
              )),
        ),
      );
      final before = await store.transaction(
        (tx) => tx.scopedChangeCursor('account'),
      );
      await sdk.putVertices([
        VertexInput(
          key: 'orders:private:1',
          value: VertexValue.string('hidden'),
        ),
        VertexInput(key: 'orders:resident', value: VertexValue.string('new')),
      ]);
      await _wait(
        'legacy resident invalidated',
        () async =>
            (await repository.readVertex(
              'account',
              'orders:resident',
              policy: OfflineReadPolicy.cacheOnly,
            )).state ==
            OfflineReadState.unknown,
      );
      await _wait(
        'legacy cursor advanced',
        () async =>
            (await store.transaction(
              (tx) => tx.scopedChangeCursor('account'),
            ))?.toJson() !=
            before!.toJson(),
      );
      cancellation.cancel();
      await canceled;
      final reopened = InMemoryOfflineStore.fromSnapshot(
        await store.exportSnapshot(),
      );
      expect(
        (await reopened.transaction(
          (tx) => tx.scopedChangeCursor('account'),
        ))!.toBytes(),
        (await store.transaction(
          (tx) => tx.scopedChangeCursor('account'),
        ))!.toBytes(),
      );
      await expectLater(
        sdk
            .watchChanges(
              prefix: 'orders:',
              projection: ChangeProjection.value,
              bootstrap: true,
            )
            .first,
        throwsA(isA<LanternPermissionDeniedException>()),
      );
      await expectLater(
        sdk.getReplicationStatus(),
        throwsA(isA<LanternPermissionDeniedException>()),
      );
      await repository.readVertex(
        'account',
        'orders:resident',
        policy: OfflineReadPolicy.serverOnly,
      );
      final denied = client(
        io.Platform.environment['LANTERN_SCOPED_WIRE_DENIED_CREDENTIAL']!,
      );
      addTearDown(denied.close);
      await expectLater(
        repository.consumeScopedChanges(
          'account',
          source: LanternScopedChangeSource(source: _source(denied)),
        ),
        throwsA(isA<OfflineRemoteFailure>()),
      );
      expect(
        await store.transaction((tx) => tx.scopedChangeCursor('account')),
        isNull,
      );
      expect(
        await store.transaction(
          (tx) => tx.hasUnknownResident(
            'account',
            const OfflineEntityKey.vertex('orders:resident'),
          ),
        ),
        isTrue,
      );
    },
    skip: url == null
        ? 'Run through the real OIDC tests/integration fixture.'
        : false,
    timeout: const Timeout(Duration(seconds: 30)),
  );

  for (final sqlite in [false, true]) {
    test(
      'automatic ${sqlite ? 'SQLite' : 'memory'} session observes and resumes authenticated public CDC',
      () async {
        final writer = _wireClient();
        addTearDown(writer.close);
        final client = _wireClient();
        addTearDown(client.close);
        final source = _source(client);
        final key = 'orders:auto:${sqlite ? 'sqlite' : 'memory'}:resident';
        await writer.putVertex(
          VertexInput(key: key, value: const StringValue('initial')),
        );
        final directory = await io.Directory.systemTemp.createTemp(
          'lantern-cdc-wire-',
        );
        addTearDown(() => directory.delete(recursive: true));
        if (sqlite) sqfliteFfiInit();
        final app = await AppDataSessionFactory(
          client: client,
          binding: source.binding,
          profile: sqlite ? AppDataProfile.durable : AppDataProfile.memory,
          legacyPartition: 'fixture-account',
          pinnedResponder: true,
          changePrefix: 'orders:',
          databaseFactory: databaseFactoryFfi,
          resolveLocation: () async => AppSqliteLocation(
            '${directory.path}/cache.db',
            SqliteInstallationPolicy(
              identity: 'isolated-cdc-wire-host-fixture',
              version: 1,
              evaluate: (_) async => SqliteInstallationEvidence.verified(
                installationId: 'host-installation',
              ),
            ),
            null,
          ),
        ).open();
        addTearDown(app.close);
        final session = app.data!;
        final values = <LanternSnapshot<Vertex>>[];
        final watch = session
            .watchVertex(
              key,
              options: const WatchOptions(pollInterval: Duration(minutes: 10)),
            )
            .listen(values.add);
        addTearDown(watch.cancel);
        String? current() => values.isEmpty
            ? null
            : (values.last.confirmed.value?.value as StringValue?)?.value;
        await _wait('initial observation', () async => current() == 'initial');
        await writer.putVertex(
          VertexInput(key: key, value: const StringValue('external')),
        );
        // The ten-minute polling deadline cannot explain this finite refresh.
        // The application never calls Get, Sync, consumeScopedChanges or drain.
        await _wait(
          'external CDC refresh',
          () async => current() == 'external',
        );
        expect(values.last.observationMode, ObservationMode.pollingObservation);
        final ticket = await session.mutations.submit(
          MutationRequest.putVertices([
            VertexInput(key: key, value: const StringValue('local')),
          ]),
        );
        await _wait(
          'local mutation observation',
          () async => current() == 'local',
        );
        await _wait(
          'local mutation handled',
          () async =>
              (await session.mutations.getStatus(
                ticket.reference.operationId,
              )).items.single.state ==
              MutationItemState.handled,
        );
        await app.suspend();
        await writer.putVertex(
          VertexInput(key: key, value: const StringValue('while-suspended')),
        );
        await app.resume();
        await _wait(
          'resumed CDC refresh',
          () async => current() == 'while-suspended',
        );
      },
      skip: url == null
          ? 'Run through the real OIDC tests/integration fixture.'
          : false,
      timeout: const Timeout(Duration(seconds: 30)),
    );
  }

  test(
    'I7 app factory resumes on transport recovery only with active vertex edge and traversal watches',
    () async {
      final writer = _wireClient();
      addTearDown(writer.close);
      const tail = 'orders:create:app-tail', head = 'orders:create:app-head';
      await writer.putVertices([
        VertexInput(key: tail, value: const StringValue('before')),
        VertexInput(key: head, value: const StringValue('head')),
      ]);
      await writer.putEdge(EdgeInput(tail: tail, head: head, weight: 2));
      final availability = _WireAvailability()..available = false;
      final client = _wireClient(availability: availability);
      addTearDown(client.close);
      final source = _source(client);
      final directory = await io.Directory.systemTemp.createTemp(
        'lantern-app-recovery-wire-',
      );
      addTearDown(() => directory.delete(recursive: true));
      sqfliteFfiInit();
      final app = await AppDataSessionFactory(
        client: client,
        binding: source.binding,
        profile: AppDataProfile.durable,
        legacyPartition: 'fixture-account',
        pinnedResponder: true,
        changePrefix: 'orders:',
        databaseFactory: databaseFactoryFfi,
        resolveLocation: () async => AppSqliteLocation(
          '${directory.path}/app.db',
          SqliteInstallationPolicy(
            identity: 'app-wire-host-fixture',
            version: 1,
            evaluate: (_) async => SqliteInstallationEvidence.verified(
              installationId: 'host-installation',
            ),
          ),
          null,
        ),
      ).open();
      addTearDown(app.close);
      final data = app.data!;
      final vertices = <LanternSnapshot<Vertex>>[],
          second = <LanternSnapshot<Vertex>>[];
      final edges = <LanternSnapshot<Edge>>[],
          graphs = <LanternTraversalSnapshot>[];
      const options = WatchOptions(pollInterval: Duration(seconds: 1));
      final first = data
          .watchVertex(tail, options: options)
          .listen(vertices.add);
      final duplicate = data
          .watchVertex(tail, options: options)
          .listen(second.add);
      final edge = data
          .watchEdge(const EdgeRef(tail, head), options: options)
          .listen(edges.add);
      final graph = data
          .watchIlluminate(
            tail,
            traversal: const BfsOptions(step: 1, fanOut: 10),
            vertexPrefix: 'orders:',
            options: options,
          )
          .listen(graphs.add);
      addTearDown(first.cancel);
      addTearDown(duplicate.cancel);
      addTearDown(edge.cancel);
      addTearDown(graph.cancel);
      final ticket = await data.mutations.submit(
        MutationRequest.putVertices([
          VertexInput(
            key: tail,
            value: const StringValue('restored'),
            expiresIn: const Duration(minutes: 5),
          ),
        ]),
      );
      await _wait('blocked transport', () async => availability.blocked > 0);
      expect(
        (await data.mutations.getStatus(ticket.reference.operationId)).terminal,
        false,
      );
      availability.available = true;
      // No Get/resume/Sync/drain/notifyReachability or application retry timer.
      try {
        await _wait(
          'automatic write and vertex',
          () async =>
              vertices.isNotEmpty &&
              (vertices.last.confirmed.value?.value as StringValue?)?.value ==
                  'restored',
        );
      } catch (_) {
        final status = await data.mutations.getStatus(
          ticket.reference.operationId,
        );
        fail(
          'Automatic recovery: item=${status.items.map((i) => '${i.state.name}/${i.failure}').join(',')}; '
          'vertex=${vertices.isEmpty ? 'none' : vertices.last.confirmed.state.name}; '
          'wire=${availability.failures}',
        );
      }
      await _wait(
        'edge and traversal',
        () async =>
            edges.isNotEmpty &&
            edges.last.confirmed.value?.weight == 2 &&
            graphs.isNotEmpty &&
            graphs.last.confirmed.value?.allEdges.isNotEmpty == true,
      );
      expect(
        (await data.mutations.getStatus(
          ticket.reference.operationId,
        )).items.single.state,
        MutationItemState.handled,
      );
      await first.cancel();
      final count = vertices.length;
      await writer.putVertex(
        VertexInput(key: tail, value: const StringValue('second-listener')),
      );
      await _wait(
        'independent second listener',
        () async =>
            second.isNotEmpty &&
            (second.last.confirmed.value?.value as StringValue?)?.value ==
                'second-listener',
      );
      expect(vertices.length, count);
      expect(client.isClosed, false);
      await duplicate.cancel();
      await edge.cancel();
      await graph.cancel();
      await app.close();
      expect(client.isClosed, true);
    },
    skip: url == null
        ? 'Run through the real OIDC tests/integration fixture.'
        : false,
    timeout: const Timeout(Duration(seconds: 30)),
  );

  test(
    'automatic public CDC permission denial pauses without polling fallback',
    () async {
      final client = _wireClient(denied: true);
      addTearDown(client.close);
      final source = _source(client);
      final session = await OfflineLanternSession.open(
        client: client,
        store: MemoryOfflineStore(),
        binding: source.binding,
        incomingChanges: source,
        durability: AdmissionDurability.memory,
      );
      addTearDown(session.close);
      final statuses = <LanternSyncStatus>[];
      final status = session.syncStatus.listen(statuses.add);
      addTearDown(status.cancel);
      await _wait(
        'permission pause',
        () async => statuses.any((value) => value.pause == SyncPause.policy),
      );
      final value = await session.getVertex('orders:auto:denied');
      expect(value.confirmed.state, ConfirmedState.unknown);
      expect(value.confirmed.cause, ObservationCause.permissionDenied);
      await session.suspend();
      await session.resume();
      expect((await session.syncStatus.first).pause, SyncPause.policy);
    },
    skip: url == null
        ? 'Run through the real OIDC tests/integration fixture.'
        : false,
    timeout: const Timeout(Duration(seconds: 30)),
  );
}

LanternClient _wireClient({
  bool denied = false,
  _WireAvailability? availability,
}) {
  final context = io.SecurityContext(
    withTrustedRoots: false,
  )..setTrustedCertificates(io.Platform.environment['LANTERN_SCOPED_WIRE_CA']!);
  return LanternClient.connect(
    Uri.parse(io.Platform.environment['LANTERN_SCOPED_WIRE_URL']!),
    httpClientFactory: () => io.HttpClient(context: context),
    interceptors: [if (availability != null) availability.intercept],
    tokenProvider: () =>
        io.Platform.environment[denied
            ? 'LANTERN_SCOPED_WIRE_DENIED_CREDENTIAL'
            : 'LANTERN_SCOPED_WIRE_CREDENTIAL'],
    defaultTimeout: const Duration(seconds: 5),
  );
}

LanternChangeSource _source(LanternClient client) {
  final binding = LanternSessionBinding.application(
    serviceOrigin: client.endpoint,
    securityDomain: 'authenticated-scoped-wire',
    accountIdentity: 'fixture-account',
    authorityBinding: 'fixture-current-authority',
  );
  return LanternChangeSource(
    route: LanternSingleResponderRoute.certified(
      client: client,
      responderId: 'fixture-direct-node',
      isCurrent: () => !client.isClosed,
    ),
    binding: binding,
    currentBinding: () => binding,
    prefix: 'orders:',
  );
}

Future<void> _wait(String phase, Future<bool> Function() ready) async {
  for (var i = 0; i < 400; i++) {
    if (await ready()) return;
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
  fail('public CDC phase $phase did not become ready');
}

final class _WireAvailability {
  bool available = true;
  int blocked = 0;
  final failures = <String>[];
  connect.AnyFn<I, O> intercept<I extends Object, O extends Object>(
    connect.AnyFn<I, O> next,
  ) => (request) async {
    if (!available) {
      blocked++;
      throw connect.ConnectException(
        connect.Code.unavailable,
        'finite transport interruption',
      );
    }
    try {
      return await next(request);
    } on connect.ConnectException catch (error) {
      if (failures.length < 8) {
        failures.add(
          '${request.spec.procedure.split('/').last}:${error.code.name}',
        );
      }
      rethrow;
    }
  };
}
