import 'dart:io' as io;

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
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
        final session = sqlite
            ? await SqliteOfflineStore.openSession(
                path: '${directory.path}/cache.db',
                client: client,
                binding: source.binding,
                incomingChanges: source,
                databaseFactory: databaseFactoryFfi,
              )
            : await OfflineLanternSession.open(
                client: client,
                store: MemoryOfflineStore(),
                binding: source.binding,
                incomingChanges: source,
                durability: AdmissionDurability.memory,
              );
        addTearDown(session.close);
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
        await session.suspend();
        await writer.putVertex(
          VertexInput(key: key, value: const StringValue('while-suspended')),
        );
        await session.resume();
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

LanternClient _wireClient({bool denied = false}) {
  final context = io.SecurityContext(
    withTrustedRoots: false,
  )..setTrustedCertificates(io.Platform.environment['LANTERN_SCOPED_WIRE_CA']!);
  return LanternClient.connect(
    Uri.parse(io.Platform.environment['LANTERN_SCOPED_WIRE_URL']!),
    httpClientFactory: () => io.HttpClient(context: context),
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
