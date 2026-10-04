import 'dart:io' as io;

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';

import 'package:lantern_example/scoped_change_source.dart';

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
      final source = LanternScopedChangeSource(
        client: sdk,
        responderId: sdk.endpoint.toString(),
        prefix: 'orders:',
      );
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
        () async =>
            (await repository.readVertex(
              'account',
              'orders:resident',
              policy: OfflineReadPolicy.cacheOnly,
            )).state ==
            OfflineReadState.unknown,
      );
      await _wait(
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
          source: LanternScopedChangeSource(
            client: denied,
            responderId: denied.endpoint.toString(),
            prefix: 'orders:',
          ),
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
}

Future<void> _wait(Future<bool> Function() ready) async {
  for (var i = 0; i < 400; i++) {
    if (await ready()) return;
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
  fail('public CDC state did not become ready');
}
