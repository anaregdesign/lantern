import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:lantern_example/app_data_session.dart';
import 'package:lantern_example/data_observations.dart';
import 'package:lantern_example/installation_policy.dart';
import 'package:lantern_example/native_credential_store.dart';
import 'package:lantern_example/native_providers.dart';
import 'package:lantern_example/native_session_controller.dart';
import 'package:lantern_example/offline_session.dart';
import 'package:sqflite/sqflite.dart' as sqflite;

import 'support/direct_launch_binding.dart';
import 'support/installed_factory_fixture.dart';
import 'support/physical_result_marker.dart';
import 'support/receipt_attestation.dart';

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  const description =
      'physical installed factories and immutable native account ownership';
  directLaunchIntegrationTest(description, () async {
    final marker = PhysicalResultMarker(
      'lantern-installed-factory-result.json',
      kind: 'physical_installed_factory_on_device_result',
      requireInstalledBinary: true,
    );
    await marker.recordPhase('setup');
    var completed = false;
    addTearDown(() async {
      if (completed) {
        final passed = binding.results[description] == 'success';
        await marker.recordOutcome(
          passed ? 'passed' : 'failed',
          passed ? 'complete' : 'framework',
          failureType: passed ? null : 'assertion',
        );
      }
    });
    try {
      expect(
        const bool.fromEnvironment('LANTERN_PHYSICAL_QUALIFICATION'),
        isTrue,
      );
      final attestation = ReceiptAttestation.fromBuild(
        target: 'integration_test/physical_installed_factory_test.dart',
        requiredScenarios: installedFactoryScenarios,
      );
      await attestation.run((proof) async {
        final run = _InstalledFactoryRun(binding, proof);
        await run.open();
        for (final entry in installedFactoryProfileScenarios.entries) {
          await marker.recordPhase(entry.value);
          await proof.verifyScenario(entry.value, () => run.profile(entry.key));
        }
        await marker.recordPhase('immutable_account_a_b_a');
        await proof.verifyScenario(
          'immutable_account_a_b_a',
          run.nativeAccounts,
        );
        await marker.recordPhase('shared_remote_wipe_before_swap');
        await proof.verifyScenario(
          'shared_remote_wipe_before_swap',
          run.sharedRemote,
        );
        await marker.recordPhase('installation_restore_ownership');
        await proof.verifyScenario(
          'installation_restore_ownership',
          run.installation,
        );
        await marker.recordPhase('cleanup');
      });
      completed = true;
    } catch (error) {
      await marker.recordOutcome(
        'failed',
        marker.phase,
        failureType: safePhysicalFailureType(error),
      );
      rethrow;
    }
  }, timeout: const Timeout(Duration(minutes: 8)));
}

final class _InstalledFactoryRun {
  _InstalledFactoryRun(this.binding, this.proof);
  final IntegrationTestWidgetsFlutterBinding binding;
  final ReceiptAttestation proof;
  final runId = const String.fromEnvironment('LANTERN_RECEIPT_RUN_ID');
  final _ownedPaths = <String>{};
  late final Uri endpoint;
  late final PhysicalSocketRelay relay;
  late final String credentialA, credentialB;
  late final LanternClient observerA, observerB;
  late final NativeSession nativeA, nativeB;
  static const allowedPrefix = String.fromEnvironment(
    'LANTERN_FACTORY_PREFIX',
    defaultValue: 'orders:',
  );
  String get root => '${allowedPrefix}t18:$runId:';
  final _remoteKeys = <String>{};

  Future<void> open() async {
    endpoint = _https(const String.fromEnvironment('LANTERN_ENDPOINT'));
    expect(allowedPrefix, startsWith('orders:'));
    expect(allowedPrefix, endsWith(':'));
    expect(
      const bool.fromEnvironment('LANTERN_OFFLINE_CDC_PINNED_RESPONDER'),
      isTrue,
    );
    relay = await PhysicalSocketRelay.open(endpoint);
    proof.registerCleanup(relay.close);
    proof.registerCleanup(() async {
      await Future.wait(_ownedPaths.map(sqflite.deleteDatabase));
    });
    credentialA = await _token(
      _https(const String.fromEnvironment('LANTERN_TOKEN_ENDPOINT')),
    );
    credentialB = await _token(
      _https(const String.fromEnvironment('LANTERN_FACTORY_B_TOKEN_ENDPOINT')),
    );
    expect(credentialA == credentialB, isFalse);
    observerA = _observer(credentialA);
    observerB = _observer(credentialB);
    nativeA = (await observerA.getNativeSession()).session;
    nativeB = (await observerB.getNativeSession()).session;
    expect(nativeA.userId, isNotEmpty);
    expect(nativeB.userId, isNotEmpty);
    expect(nativeA.userId, isNot(nativeB.userId));
    expect(nativeA.personalPrefix, isNotEmpty);
    expect(nativeB.personalPrefix, isNotEmpty);
    expect(nativeA.personalPrefix, isNot(nativeB.personalPrefix));
    final minimumExpiry = DateTime.now().toUtc().add(
      const Duration(minutes: 10),
    );
    expect(nativeA.expiresAt.isAfter(minimumExpiry), isTrue);
    expect(nativeB.expiresAt.isAfter(minimumExpiry), isTrue);
    expect((
      nativeA.principal.issuer,
      nativeA.principal.subject,
    ), isNot((nativeB.principal.issuer, nativeB.principal.subject)));
    proof.registerCleanup(() async {
      if (_remoteKeys.isNotEmpty) {
        await observerA.deleteVertices(_remoteKeys);
      }
    });
    proof.registerCleanup(() async {
      runApp(const SizedBox.shrink());
      await binding.pump();
    });
  }

  LanternClient _observer(String credential) {
    final client = LanternClient.connect(
      endpoint,
      tokenProvider: () => credential,
      retryPolicy: const RetryPolicy(maxAttempts: 1),
      defaultTimeout: const Duration(seconds: 10),
    );
    proof.registerCleanup(client.close);
    return client;
  }

  _OwnedClient _client([bool accountB = false]) {
    final owned = _OwnedClient(
      endpoint,
      accountB ? credentialB : credentialA,
      relay,
    );
    proof.registerCleanup(owned.close);
    return owned;
  }

  LanternSessionBinding _identity(String name, {bool accountB = false}) =>
      LanternSessionBinding.native(
        accountB ? nativeB : nativeA,
        securityDomain: 'installed-factory:$runId:$name',
      );

  String _partition(String name, NativeSession session) =>
      't18:$runId:$name:${nativeAccountPartition(endpoint, session)}';

  Future<AppSqliteLocation> _location(
    LanternSessionBinding identity,
    String partition, {
    LegacyInstallationAssertion? assertLegacy,
  }) async {
    final location = await MobileInstallationPolicy(
      assertLegacyInstallation: assertLegacy,
    ).resolve(binding: identity, legacyPartition: partition);
    _ownPath(location.path);
    return location;
  }

  void _ownPath(String path) {
    _ownedPaths.add(path);
  }

  AppDataSessionFactory _factory({
    required _OwnedClient owned,
    required LanternSessionBinding identity,
    required AppDataProfile profile,
    required String partition,
    required PhysicalSessionSchedule schedule,
    AppSqliteLocation? location,
    AppDataLifetime? lifetime,
  }) => AppDataSessionFactory(
    client: owned.client,
    binding: identity,
    profile: profile,
    legacyPartition: partition,
    lifetime: lifetime,
    pinnedResponder: true,
    changePrefix: root,
    schedule: schedule.call,
    resolveLocation: location == null ? null : () async => location,
  );

  Future<void> profile(AppDataProfile profile) async {
    final name = profile.name, prefix = '$root${profile.name}:';
    final alice = '${prefix}alice', bob = '${prefix}bob';
    _remoteKeys.addAll([alice, bob]);
    await observerA.putVertices([
      VertexInput(key: alice, value: const StringValue('initial')),
      VertexInput(key: bob, value: const StringValue('endpoint')),
    ]);
    await observerA.putEdge(EdgeInput(tail: alice, head: bob, weight: 1));
    final owned = _client(), schedule = PhysicalSessionSchedule();
    final identity = _identity(name);
    final partition = _partition(name, nativeA);
    final durable =
        profile == AppDataProfile.durable ||
        profile == AppDataProfile.durableWithoutReadCache;
    final location = durable ? await _location(identity, partition) : null;
    final factory = _factory(
      owned: owned,
      identity: identity,
      profile: profile,
      partition: partition,
      schedule: schedule,
      location: location,
    );
    final opening = factory.open();
    expect(identical(opening, factory.open()), isTrue);
    final owner = await opening;
    proof.registerCleanup(owner.close);
    expect(owner.restoreHeld, isNull);
    final data = owner.data!;
    final events = <LanternSnapshot<Vertex>>[];
    final watch = data.watchVertex(alice).listen(events.add);
    proof.registerCleanup(watch.cancel);
    final traversals = <LanternTraversalSnapshot>[];
    final traversalWatch = data
        .watchIlluminate(
          alice,
          traversal: const BfsOptions(step: 2, fanOut: 10),
          vertexPrefix: prefix,
        )
        .listen(traversals.add);
    proof.registerCleanup(traversalWatch.cancel);
    runApp(
      MaterialApp(
        home: DataObservationsScreen(
          key: ObjectKey(owner),
          session: owner,
          prefix: prefix,
        ),
      ),
    );
    await _until(
      () =>
          _visible('initial') &&
          _textHas('live-edge', 'present: 1.0') &&
          _textHas('live-traversal', '1 edges'),
    );
    expect(find.text('Sync'), findsNothing);
    expect(find.text('Get'), findsNothing);
    final cached = await data.getVertex(
      alice,
      options: const GetOptions(source: ReadSource.cacheOnly),
    );
    expect(
      cached.confirmed.state,
      profile == AppDataProfile.direct ||
              profile == AppDataProfile.durableWithoutReadCache
          ? ConfirmedState.unknown
          : ConfirmedState.present,
    );
    if (location != null) {
      await expectLater(
        SqliteOfflineStore.openAutomatic(path: location.path),
        throwsA(isA<OfflineOwnerConflictException>()),
      );
    }

    final rejected = relay.rejectedConnections, cut = relay.cutConnections;
    relay.setAvailable(false);
    final ui = DirectLaunchWidgetController(binding);
    await ui.ensureVisible(find.text('Submit locally'));
    await binding.pump();
    await ui.tap(find.text('Submit locally'));
    await _until(() => relay.rejectedConnections > rejected);
    if (profile != AppDataProfile.direct) {
      await _until(() => events.any((event) => event.pending.count == 1));
    }
    await observerA.putEdge(EdgeInput(tail: alice, head: bob, weight: 2));
    // No Get, resume, reachability notification or manual drain after this cut.
    relay.setAvailable(true);
    if (profile == AppDataProfile.direct) {
      await _pause(const Duration(seconds: 1));
      expect(
        (await observerA.getVertex(alice)).value,
        const StringValue('initial'),
      );
      await ui.tap(find.text('Put'));
    } else {
      final durability = durable ? 'durable' : 'memory';
      await _until(
        () => find.text('$durability: handled').evaluate().isNotEmpty,
      );
    }
    await _until(
      () =>
          _visible('Hello Lantern') &&
          _textHas('live-edge', 'present: 2.0') &&
          traversals.any(
            (event) =>
                event.confirmed.value?.edge(alice, bob)?.weight == 2 &&
                event.confirmed.value?.vertices[alice]?.value ==
                    const StringValue('Hello Lantern'),
          ) &&
          events.any(
            (event) =>
                event.pending.count == 0 &&
                event.confirmed.state == ConfirmedState.present &&
                event.view?.value == const StringValue('Hello Lantern'),
          ),
    );
    expect(
      (await observerA.getVertex(alice)).value,
      const StringValue('Hello Lantern'),
    );
    expect(relay.cutConnections, greaterThan(cut));

    await owner.suspend();
    expect(schedule.activeAlarms, 0);
    if (profile != AppDataProfile.direct) {
      final ticket = await data.mutations.submit(
        MutationRequest.putVertices([
          VertexInput(key: alice, value: const StringValue('after-resume')),
        ]),
      );
      expect(
        ticket.durability,
        durable ? AdmissionDurability.durable : AdmissionDurability.memory,
      );
      await _pause(const Duration(milliseconds: 150));
      expect(
        (await observerA.getVertex(alice)).value,
        const StringValue('Hello Lantern'),
      );
      await owner.resume();
      _applied(await _pumping(data.mutations.reobserve(ticket.reference)));
      await _until(() => _visible('after-resume'));
    } else {
      await owner.resume();
    }
    runApp(const SizedBox.shrink());
    await binding.pump();
    final closing = owner.close();
    expect(identical(closing, owner.close()), isTrue);
    await closing;
    final eventCount = events.length,
        traversalCount = traversals.length,
        tokenReads = owned.tokenReads;
    await _pause(const Duration(milliseconds: 150));
    expect(events, hasLength(eventCount));
    expect(traversals, hasLength(traversalCount));
    expect(owned.tokenReads, tokenReads);
    expect(schedule.activeAlarms, 0);
    await owned.close();
    await watch.cancel();
    await traversalWatch.cancel();
    if (location != null) {
      final released = await SqliteOfflineStore.openAutomatic(
        path: location.path,
      );
      await released.close();
    }
    relay.check();
  }

  bool _visible(String value) => _textHas('live-vertex', value);

  bool _textHas(String key, String value) {
    final found = find.byKey(Key(key)).evaluate();
    return found.length == 1 &&
        (found.single.widget as Text).data!.contains(value);
  }

  Future<void> nativeAccounts() async {
    final credentials = SecureNativeCredentialStore(endpoint);
    expect(
      await credentials.read(),
      isNull,
      reason: 'Never overwrite an existing credential',
    );
    proof.registerCleanup(credentials.clear);
    final owned = <_OwnedClient>[], schedules = <PhysicalSessionSchedule>[];
    final locations = <String, AppSqliteLocation>{};
    final controller = NativeSessionController(
      endpoint: endpoint,
      providers: PlatformNativeProviders.instance,
      store: credentials,
      createClient: (credential) {
        expect(credential == credentialA || credential == credentialB, isTrue);
        final client = _OwnedClient(endpoint, credential!, relay);
        owned.add(client);
        return client.client;
      },
      openData: (client, session, fresh, lifetime) async {
        expect(fresh, isFalse, reason: 'Restore is not fresh provider login');
        final identity = LanternSessionBinding.native(
          session,
          securityDomain: 'installed-factory:$runId:native-accounts',
        );
        final partition = _partition('native-accounts', session);
        final location = locations[partition] ??= await _location(
          identity,
          partition,
        );
        final schedule = PhysicalSessionSchedule();
        schedules.add(schedule);
        return _factory(
          owned: owned.last,
          identity: identity,
          profile: AppDataProfile.durable,
          partition: partition,
          schedule: schedule,
          location: location,
          lifetime: lifetime,
        ).open(activate: false);
      },
    );
    proof.registerCleanup(() async {
      await controller.close();
      controller.dispose();
      for (final client in owned) {
        await client.close();
      }
      expect(schedules.every((s) => s.activeAlarms == 0), isTrue);
    });
    Future<NativeAccount> restore(
      String credential,
      NativeSession session,
    ) async {
      await credentials.write(
        StoredNativeCredential.fromSession(credential, session),
      );
      await controller.restore();
      expect(
        controller.account,
        isNotNull,
        reason: 'Verified native restore must open its factory',
      );
      return controller.account!;
    }

    final a = await restore(credentialA, nativeA);
    await a.dataSession!.suspend();
    final key = '${root}native:retained';
    _remoteKeys.add(key);
    final expires = DateTime.now().toUtc().add(const Duration(minutes: 5));
    final ticket = await a.dataSession!.data!.mutations.submit(
      MutationRequest.putVertices([
        VertexInput(
          key: key,
          value: const StringValue('account-a'),
          expiresAt: expires,
        ),
      ]),
    );
    final oldEvents = <LanternSnapshot<Vertex>>[];
    final oldWatch = a.dataSession!.data!
        .watchVertex(key)
        .listen(oldEvents.add);
    proof.registerCleanup(oldWatch.cancel);
    await controller.signOut(revoke: false);
    expect(a.client.isClosed, isTrue);
    expect(a.cancellation.isCanceled, isTrue);
    expect(await credentials.read(), isNull);
    final oldEventCount = oldEvents.length,
        oldTokenReads = owned.first.tokenReads;
    final b = await restore(credentialB, nativeB);
    expect(identical(a.client, b.client), isFalse);
    expect(b.partition, isNot(a.partition));
    expect(
      (await b.dataSession!.data!.getVertex(
        key,
        options: const GetOptions(source: ReadSource.cacheOnly),
      )).view,
      isNull,
    );
    expect(
      (await b.dataSession!.data!.mutations.getStatus(
        ticket.reference.operationId,
      )).retention,
      MutationRetention.unknown,
    );
    final bKey = '${root}native:b';
    _remoteKeys.add(bKey);
    await b.dataSession!.data!.putVertex(
      VertexInput(key: bKey, value: const StringValue('account-b')),
    );
    await expectLater(
      observerA.getVertex(key),
      throwsA(isA<LanternNotFoundException>()),
    );
    expect(oldEvents, hasLength(oldEventCount));
    expect(owned.first.tokenReads, oldTokenReads);
    await controller.signOut(revoke: false);
    final again = await restore(credentialA, nativeA);
    expect(again.partition, a.partition);
    expect(b.client.isClosed, isTrue);
    _applied(
      await again.dataSession!.data!.mutations
          .reobserve(ticket.reference)
          .timeout(const Duration(seconds: 45)),
    );
    final retained = await observerA.getVertex(key);
    expect(retained.value, const StringValue('account-a'));
    expect(retained.expiration, expires);
    await expectLater(
      Future.sync(() => a.dataSession!.data!.getVertex(key)),
      throwsA(isA<OfflineDisposedException>()),
    );
    await controller.close();
    expect(owned.every((c) => c.closes == 1), isTrue);
    expect(schedules.every((s) => s.activeAlarms == 0), isTrue);
  }

  Future<void> sharedRemote() async {
    final identity = _identity('shared-remote');
    final location = await _location(
      identity,
      _partition('shared-remote', nativeA),
    );
    final store = await SqliteOfflineStore.open(path: location.path);
    proof.registerCleanup(store.close);
    var credential = credentialA, wiped = false, tokenReads = 0;
    final client = LanternClient.connect(
      endpoint,
      tokenProvider: () {
        if (credential == credentialB) expect(wiped, isTrue);
        tokenReads++;
        return credential;
      },
    );
    proof.registerCleanup(client.close);
    final repository = OfflineLanternRepository(
      store: store,
      remote: LanternClientOfflineRemote(client),
    );
    proof.registerCleanup(repository.dispose);
    final oldKey = '${root}shared:a', newKey = '${root}shared:b';
    _remoteKeys.addAll([oldKey, newKey]);
    await repository.putVertex(
      partitionId: 'shared-a',
      input: VertexInput(
        key: oldKey,
        value: const StringValue('must-not-transfer'),
      ),
    );
    await repository.wipePartition('shared-a');
    expect(await repository.listPending('shared-a'), isEmpty);
    expect(tokenReads, 0);
    wiped = true;
    credential = credentialB;
    // This is the separate legacy mutable-remote barrier, not factory recovery.
    expect(await repository.drain('shared-a'), 0);
    expect(tokenReads, 0);
    await repository.putVertex(
      partitionId: 'shared-b',
      input: VertexInput(key: newKey, value: const StringValue('new-owner')),
    );
    expect(await repository.drain('shared-b'), 1);
    expect(tokenReads, greaterThan(0));
    expect(
      (await observerB.getVertex(newKey)).value,
      const StringValue('new-owner'),
    );
    await expectLater(
      observerA.getVertex(oldKey),
      throwsA(isA<LanternNotFoundException>()),
    );
    await repository.dispose();
    await store.close();
    final reopened = await SqliteOfflineStore.open(path: location.path);
    try {
      expect(
        await reopened.transaction((tx) => tx.outbox('shared-a')),
        isEmpty,
      );
    } finally {
      await reopened.close();
    }
  }

  Future<void> installation() async {
    final identity = _identity('installation');
    final partition = _partition('installation', nativeA);
    final paths = await MobileInstallationPolicy.channel
        .invokeMapMethod<String, String>('paths', {
          'filename': offlineDatabaseFileName(endpoint, partition),
        });
    expect(paths!.keys.toSet(), {'fresh', 'legacy'});
    final legacy = paths['legacy']!, fresh = paths['fresh']!;
    expect(legacy, isNot(fresh));
    for (final path in [legacy, fresh]) {
      expect(await File(path).exists(), isFalse);
      _ownPath(path);
    }
    final store = await SqliteOfflineStore.open(path: legacy);
    final repository = OfflineLanternRepository(
      store: store,
      remote: LanternClientOfflineRemote(observerA),
    );
    final key = '${root}installation:adopted';
    _remoteKeys.add(key);
    late OfflineWriteHandle original;
    try {
      original = await repository.putVertex(
        partitionId: partition,
        input: VertexInput(
          key: key,
          value: const StringValue('original-intent'),
        ),
      );
    } finally {
      await repository.dispose();
      await store.close();
    }
    final originalBytes = await File(legacy).readAsBytes();
    final originalHash = sha256.convert(originalBytes);
    Future<(AppDataSession, _OwnedClient)> open({
      bool verified = false,
      bool activate = false,
    }) async {
      final owned = _client();
      final location = await _location(
        identity,
        partition,
        // Independent fixture provenance: this exact DB was created and closed
        // above in this installed process. Login or a new sentinel is not proof.
        assertLegacy: verified
            ? (request) async =>
                  request.path ==
                      SqliteOfflineStore.canonicalAutomaticPath(legacy) &&
                  sha256.convert(await File(legacy).readAsBytes()) ==
                      originalHash
            : null,
      );
      final factory = _factory(
        owned: owned,
        identity: identity,
        profile: AppDataProfile.durable,
        partition: partition,
        schedule: PhysicalSessionSchedule(),
        location: location,
      );
      final owner = await factory.open(activate: activate);
      proof.registerCleanup(owner.close);
      return (owner, owned);
    }

    final (held, heldClient) = await open();
    expect(held.data, isNull);
    expect(held.restoreHeld, SqliteRestoreReason.legacyAssertionMissing);
    expect(heldClient.tokenReads, 0);
    expect(await File(legacy).readAsBytes(), originalBytes);
    expect(await File(fresh).exists(), isFalse);
    await closeAccountData(held, heldClient.client);
    final (adopted, adoptedClient) = await open(verified: true);
    final retained = await (adopted.data! as OfflineLanternSession)
        .inspectLegacyOperation(original.operationId);
    expect(retained!.operationId, original.operationId);
    expect(retained.isTerminal, isFalse);
    expect(adoptedClient.tokenReads, 0);
    await closeAccountData(adopted, adoptedClient.client);
    final (reopened, reopenedClient) = await open(activate: true);
    await _untilAsync(() async {
      final status = await (reopened.data! as OfflineLanternSession)
          .inspectLegacyOperation(original.operationId);
      return status!.isTerminal;
    });
    expect(
      (await observerA.getVertex(key)).value,
      const StringValue('original-intent'),
    );
    await closeAccountData(reopened, reopenedClient.client);

    // Corrupt only this run's closed fixture DB, never the native sentinel or
    // Keychain. A matching login must not repair or replace restored metadata.
    final database = await sqflite.openDatabase(legacy, singleInstance: false);
    try {
      await database.update('installation_binding', {
        'installation_id': '0' * 64,
      });
    } finally {
      await database.close();
    }
    final corruptBytes = await File(legacy).readAsBytes();
    final (mismatch, mismatchClient) = await open();
    expect(mismatch.data, isNull);
    expect(mismatch.restoreHeld, SqliteRestoreReason.installationMismatch);
    expect(mismatchClient.tokenReads, 0);
    expect(await File(legacy).readAsBytes(), corruptBytes);
    await closeAccountData(mismatch, mismatchClient.client);
    await File(fresh).writeAsBytes([0], flush: true);
    await expectLater(
      _location(identity, partition),
      throwsA(
        isA<SqliteStartupException>().having(
          (error) => error.reason,
          'reason',
          SqliteStartupFailure.scopeConflict,
        ),
      ),
    );
    expect(await File(legacy).readAsBytes(), corruptBytes);
    expect(await File(fresh).readAsBytes(), [0]);
  }

  Future<void> _pause(Duration duration) async {
    await Future<void>.delayed(duration);
    await binding.pump();
  }

  Future<void> _until(bool Function() complete) async {
    final deadline = DateTime.now().add(const Duration(seconds: 45));
    while (!complete()) {
      if (DateTime.now().isAfter(deadline)) {
        fail('T18 bounded transition timed out');
      }
      await _pause(const Duration(milliseconds: 25));
    }
  }

  Future<void> _untilAsync(Future<bool> Function() complete) async {
    final deadline = DateTime.now().add(const Duration(seconds: 45));
    while (!await complete()) {
      if (DateTime.now().isAfter(deadline)) {
        fail('T18 bounded retained status timed out');
      }
      await _pause(const Duration(milliseconds: 25));
    }
  }

  Future<T> _pumping<T>(Future<T> operation) async {
    var complete = false;
    T? value;
    Object? failure;
    StackTrace? trace;
    operation.then<void>(
      (result) {
        value = result;
        complete = true;
      },
      onError: (Object error, StackTrace stack) {
        failure = error;
        trace = stack;
        complete = true;
      },
    );
    await _until(() => complete);
    if (failure != null) Error.throwWithStackTrace(failure!, trace!);
    return value as T;
  }
}

final class _OwnedClient {
  _OwnedClient(Uri endpoint, String credential, PhysicalSocketRelay relay) {
    client = LanternClient.connect(
      endpoint,
      tokenProvider: () {
        expect(closes, 0, reason: 'Retired credentials cannot be reused');
        tokenReads++;
        return credential;
      },
      httpClientFactory: relay.createHttpClient,
      retryPolicy: const RetryPolicy(maxAttempts: 1),
      defaultTimeout: const Duration(seconds: 5),
      onClose: () async {
        closes++;
      },
    );
  }
  late final LanternClient client;
  int tokenReads = 0, closes = 0;
  Future<void> close() async {
    await client.close();
    expect(closes, 1);
  }
}

Uri _https(String value) {
  final uri = Uri.tryParse(value);
  if (uri == null ||
      uri.scheme != 'https' ||
      uri.host.isEmpty ||
      uri.userInfo.isNotEmpty ||
      uri.hasFragment) {
    throw StateError('T18 requires a platform-trusted HTTPS fixture');
  }
  return uri;
}

Future<String> _token(Uri uri) async {
  final http = HttpClient()..connectionTimeout = const Duration(seconds: 10);
  try {
    final request = await http.getUrl(uri).timeout(const Duration(seconds: 10));
    request.followRedirects = false;
    final response = await request.close().timeout(const Duration(seconds: 10));
    final bytes = <int>[];
    await for (final chunk in response.timeout(const Duration(seconds: 10))) {
      bytes.addAll(chunk);
      if (bytes.length > 32 * 1024) {
        throw StateError('T18 token response too large');
      }
    }
    if (response.statusCode != HttpStatus.ok) {
      throw StateError('T18 token endpoint failed');
    }
    final value = jsonDecode(utf8.decode(bytes));
    final token = value is Map<String, Object?> ? value['access_token'] : null;
    if (token is! String ||
        RegExp(r'lnu1_[A-Za-z0-9_-]{43}').matchAsPrefix(token)?.end !=
            token.length) {
      throw StateError('T18 requires an original native credential');
    }
    return token;
  } finally {
    http.close(force: true);
  }
}

void _applied(MutationObservation<PutOutcome> observation) {
  expect(observation.retention, MutationRetention.retained);
  expect(
    observation.reply!.items.single,
    isA<MutationKnownEffect<PutOutcome>>().having(
      (reply) => reply.effect,
      'effect',
      PutOutcome.appliedAndLive,
    ),
  );
}
