import 'dart:async';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';
import 'package:test/test.dart';

import 'helpers.dart' show FakeOfflineRemote, offlineReceiptCapability;
import 'support/session_clock.dart';

final endpoint = Uri.parse('http://localhost:6380');
LanternSessionBinding binding({
  String account = 'account',
  DateTime? expires,
}) => LanternSessionBinding.application(
  serviceOrigin: endpoint,
  securityDomain: 'domain',
  accountIdentity: account,
  authorityBinding: 'authority',
  expiresAt: expires,
);
VertexInput vertex(String key, {DateTime? expires}) => VertexInput(
  key: key,
  value: VertexValue.string('value'),
  expiresAt: expires,
);

final class MutationRemote implements OfflineBoundMutationRemote {
  int calls = 0;
  Future<void> Function(OfflineOutboxRecord)? before, after;
  final List<OfflineOutboxRecord> sent = [];
  @override
  Future<MutationReply<Object?>> sendBoundMutation(
    OfflineOutboxRecord record, {
    required void Function() beforeDispatch,
    required void Function() beforePublication,
    LanternCancellationToken? cancellation,
  }) async {
    await before?.call(record);
    beforeDispatch();
    calls++;
    sent.add(record);
    await after?.call(record);
    beforePublication();
    return const MutationKnownEffect(PutOutcome.appliedAndLive);
  }
}

final class Harness {
  final clock = Clock();
  final store = MemoryOfflineStore();
  final remote = MutationRemote();
  final receipt = FakeOfflineRemote();
  late final LanternClient client;
  late final LanternSessionBinding identity;
  final handles = <OfflineLanternSession>[];
  Harness({
    TokenProvider? token,
    LanternTransport? transport,
    DateTime? expires,
  }) {
    identity = binding(expires: expires);
    receipt.receiptCapability = offlineReceiptCapability(serverNow: clock.now);
    client = LanternClient.connect(
      endpoint,
      allowInsecure: true,
      clock: clock.call,
      tokenProvider: token,
      transport: transport ?? FakeTransportBuilder().build(),
    );
  }
  Future<OfflineLanternSession> open({
    bool parentRemote = false,
    bool transferAllowed = true,
    bool restoreHeld = false,
  }) async {
    final handle = await OfflineLanternSession.openStore(
      client: client,
      storeKey: store,
      createStore: () async => store,
      closeStore: (_) async {},
      binding: identity,
      durability: AdmissionDurability.memory,
      clock: clock.call,
      schedule: clock.schedule,
      transferAllowed: transferAllowed,
      restoreHeld: restoreHeld,
      receiptRemote: receipt,
      mutationRemote: parentRemote ? null : remote,
    );
    handles.add(handle);
    return handle;
  }

  Future<void> close() async {
    for (final h in handles.reversed) {
      await h.close();
    }
  }

  Future<List<OfflineOutboxRecord>> rows() =>
      store.transaction((tx) => tx.outbox(identity.stableIdentity));
  Future<void> tick([int ms = 50]) => clock.advance(Duration(milliseconds: ms));
}

void main() {
  test(
    'I6 failed store close retains first failure and prevents replacement',
    () async {
      final store = MemoryOfflineStore();
      final first = StateError('store close');
      var stores = 0, transports = 0, creates = 0;
      final client = LanternClient.connect(
        endpoint,
        allowInsecure: true,
        transport: FakeTransportBuilder().build(),
        onClose: () async {
          transports++;
          throw StateError('transport close');
        },
      );
      Future<OfflineLanternSession> open() => OfflineLanternSession.openStore(
        client: client,
        storeKey: store,
        binding: binding(),
        createStore: () async {
          creates++;
          return store;
        },
        closeStore: (_) async {
          stores++;
          throw first;
        },
        durability: AdmissionDurability.memory,
        transferAllowed: false,
      );
      final session = await open();
      final closed = session.close();
      expect(identical(closed, session.close()), isTrue);
      await expectLater(closed, throwsA(same(first)));
      await expectLater(session.close(), throwsA(same(first)));
      await expectLater(open(), throwsA(isA<OfflineOwnerConflictException>()));
      expect((creates, stores, transports), (1, 1, 1));
    },
  );
  test(
    'O1 matching handles share delivery; one close continues, last closes transport',
    () async {
      final h = Harness();
      addTearDown(h.close);
      final one = await h.open(), two = await h.open();
      final ticket = await one.mutations.submit(
        MutationRequest.putVertices([vertex('v')]),
      );
      await one.close();
      expect(h.client.isClosed, false);
      await h.tick();
      expect(h.remote.calls, 1);
      expect(
        (await two.mutations.getStatus(
          ticket.reference.operationId,
        )).items.single.state,
        MutationItemState.handled,
      );
      await two.close();
      expect(h.client.isClosed, true);
    },
  );

  test(
    'O1 closing one handle joins its waiter while sibling delivery continues',
    () async {
      final h = Harness();
      addTearDown(h.close);
      final first = await h.open(), second = await h.open();
      final ticket = await first.mutations.submit(
        MutationRequest.putVertices([vertex('v')]),
        options: const MutationOptions(wait: WaitOptions()),
      );
      final observed = expectLater(
        ticket.completion,
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.closed,
          ),
        ),
      );
      await first.close();
      await observed;
      expect((await h.rows()).length, 1);
      expect(h.client.isClosed, false);
      await h.tick();
      expect(
        (await second.mutations.getStatus(
          ticket.reference.operationId,
        )).items.single.state,
        MutationItemState.handled,
      );
    },
  );

  test(
    'O3 graceful last close joins a late reply and returns only its own lease',
    () async {
      final h = Harness();
      addTearDown(h.close);
      final gate = Completer<void>();
      h.remote.after = (_) => gate.future;
      final client = await h.open();
      await client.mutations.submit(MutationRequest.putVertices([vertex('v')]));
      await h.tick();
      final before = (await h.rows()).single;
      expect(before.leaseOwner, isNotNull);
      expect(before.mayHaveDispatched, true);
      final closing = client.close();
      await flush();
      expect(h.client.isClosed, false);
      gate.complete();
      await closing;
      final after = (await h.rows()).single;
      expect(after.recordId, before.recordId);
      expect(after.mayHaveDispatched, true);
      expect(after.leaseOwner, isNull);
      expect(after.leaseUntil, isNull);
      expect(after.state, OfflineOutboxState.enqueued);
      expect(h.client.isClosed, true);
      expect(h.clock.active, 0);
    },
  );

  test(
    'automatic retry progresses foreground outage without Get or Sync',
    () async {
      final h = Harness();
      addTearDown(h.close);
      var unavailable = true;
      h.remote.after = (_) async {
        if (unavailable) {
          throw const OfflineRemoteFailure(
            OfflineRemoteErrorKind.unavailable,
            'fixture',
          );
        }
      };
      final client = await h.open();
      final expiration = h.clock.now.add(const Duration(minutes: 5));
      await client.mutations.submit(
        MutationRequest.putVertices([vertex('v', expires: expiration)]),
      );
      await h.tick();
      final first = (await h.rows()).single;
      expect(first.mayHaveDispatched, true);
      unavailable = false;
      await h.tick(1000);
      expect(h.remote.calls, 2);
      expect(await h.rows(), isEmpty);
      expect(h.remote.sent.map((r) => r.recordId).toSet(), {first.recordId});
      expect(
        h.remote.sent.every((r) => r.intent.expiration == expiration),
        true,
      );
    },
  );

  test(
    'C1 real parent token wait cancels without wire; resume uses new work',
    () async {
      final token = Completer<String?>();
      var tokens = 0, wires = 0;
      final h = Harness(
        token: () {
          tokens++;
          return tokens == 1 ? token.future : Future.value('new');
        },
        transport: FakeTransportBuilder()
            .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
              LanternService.putVertices,
              (_, _) {
                wires++;
                return graph.PutVerticesResponse(
                  outcomes: [graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE],
                );
              },
            )
            .build(),
      );
      addTearDown(h.close);
      final client = await h.open(parentRemote: true);
      await client.mutations.submit(MutationRequest.putVertices([vertex('v')]));
      await h.tick();
      expect(tokens, 1);
      final before = (await h.rows()).single;
      await client.suspend().timeout(const Duration(seconds: 2));
      token.complete('old');
      await flush();
      expect(wires, 0);
      final retained = (await h.rows()).single;
      expect(retained.recordId, before.recordId);
      expect(retained.mayHaveDispatched, true);
      expect(retained.state, OfflineOutboxState.enqueued);
      await client.resume();
      await h.tick();
      expect(wires, 1);
      expect(tokens, 2);
      expect(await h.rows(), isEmpty);
    },
  );

  test(
    'C1 committed account wipe ends old token-wait lifetime before wire',
    () async {
      final token = Completer<String?>();
      var wires = 0;
      final h = Harness(
        token: () => token.future,
        transport: FakeTransportBuilder()
            .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
              LanternService.putVertices,
              (_, _) {
                wires++;
                return graph.PutVerticesResponse();
              },
            )
            .build(),
      );
      addTearDown(h.close);
      final client = await h.open(parentRemote: true);
      await client.mutations.submit(MutationRequest.putVertices([vertex('v')]));
      await h.tick();
      await h.store.transaction(
        (tx) => tx.wipePartition(h.identity.stableIdentity),
      );
      await flush();
      token.complete('old');
      await flush();
      expect(wires, 0);
      expect(h.client.isClosed, true);
      expect(
        () => client.getVertex('v'),
        throwsA(isA<OfflineDisposedException>()),
      );
    },
  );

  test(
    'C2 auth pause retains admission and expires local TTL; verified event alone resumes',
    () async {
      final h = Harness();
      addTearDown(h.close);
      var denied = true, installed = 0;
      h.remote.after = (_) async {
        if (denied) {
          throw const OfflineRemoteFailure(
            OfflineRemoteErrorKind.unauthenticated,
            'fixture',
          );
        }
      };
      final client = await h.open();
      await client.mutations.submit(
        MutationRequest.putVertices([vertex('first')]),
      );
      await h.tick();
      expect(
        await h.store.transaction(
          (tx) => tx.replayPausedForAuth(h.identity.stableIdentity),
        ),
        true,
      );
      expect(
        await h.store.transaction(
          (tx) => tx.pausedCredentialGeneration(h.identity.stableIdentity),
        ),
        0,
      );
      final expired = await client.mutations.submit(
        MutationRequest.putVertices([
          vertex(
            'expires',
            expires: h.clock.now.add(const Duration(seconds: 1)),
          ),
        ]),
      );
      final calls = h.remote.calls;
      await h.tick(2000);
      expect(h.remote.calls, calls);
      expect(
        (await client.mutations.getStatus(
          expired.reference.operationId,
        )).items.single.state,
        MutationItemState.failed,
      );
      denied = false;
      await client.resume();
      await h.tick();
      expect(h.remote.calls, calls);
      final event = OfflineVerifiedCredentials(
        binding: h.identity,
        install: () {
          installed++;
        },
      );
      expect(await client.acceptVerifiedCredentials(event), true);
      await h.tick();
      expect(h.remote.calls, calls + 1);
      expect(installed, 1);
      expect(await client.acceptVerifiedCredentials(event), false);
      expect(await h.rows(), isEmpty);
    },
  );
  for (final stage in ['preparation', 'status', 'marker', 'publication']) {
    test(
      'C1 $stage stop joins and resumes original durable evidence status-first',
      () async {
        final h = Harness();
        addTearDown(h.close);
        final gate = Completer<void>();
        var entered = false;
        Future<void> hold() {
          entered = true;
          return gate.future;
        }

        if (stage == 'preparation') {
          h.receipt.beforeReceiptPreparationReturn = hold;
        }
        if (stage == 'status') {
          h.receipt.beforeReceiptStatusReturn = (_) => hold();
        }
        if (stage == 'marker') h.remote.before = (_) => hold();
        if (stage == 'publication') h.remote.after = (_) => hold();
        final client = await h.open();
        final expiration = h.clock.now.add(const Duration(minutes: 5));
        final ticket = await client.mutations.submit(
          MutationRequest.putVerticesIfAbsent([
            vertex('conditional', expires: expiration),
          ]),
        );
        await h.tick();
        expect(entered, true);
        final original = (await h.rows()).single;
        final stopped = client.suspend();
        await flush();
        gate.complete();
        await stopped.timeout(const Duration(seconds: 2));
        final retained = (await h.rows()).single;
        expect(retained.recordId, original.recordId);
        expect(retained.intent.expiration, expiration);
        expect(retained.mayHaveDispatched, original.mayHaveDispatched);
        expect(retained.receipt?.operationId, original.receipt?.operationId);
        expect(
          (await client.mutations.getStatus(
            ticket.reference.operationId,
          )).items.single.state,
          MutationItemState.stillPending,
        );
        final statusBefore = h.receipt.receiptStatusCalls;
        await client.resume();
        await h.tick();
        expect(await h.rows(), isEmpty);
        expect(h.receipt.receiptStatusCalls, greaterThan(statusBefore));
        expect(h.receipt.receiptPrepareCalls, stage == 'preparation' ? 2 : 1);
        if (original.receipt != null) {
          expect(
            h.receipt.receiptStatusIds.last,
            original.receipt!.operationId,
          );
        }
      },
    );
  }

  test(
    'C2 auth signal cancels sibling send/read without self-join or late publication',
    () async {
      final authGate = Completer<void>(),
          sendGate = Completer<void>(),
          readGate = Completer<void>();
      var readCalls = 0, authenticated = false;
      final h = Harness(
        transport: FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (request, _) async {
                readCalls++;
                await readGate.future;
                return graph.GetVerticesResponse(missing: request.keys);
              },
            )
            .build(),
      );
      addTearDown(h.close);
      h.remote.after = (r) async {
        if (authenticated) return;
        if (r.intent.key.vertexKey == 'auth') {
          await authGate.future;
          throw const OfflineRemoteFailure(
            OfflineRemoteErrorKind.unauthenticated,
            'fixture',
          );
        }
        await sendGate.future;
      };
      final client = await h.open();
      await client.mutations.submit(
        MutationRequest.putVertices([vertex('auth'), vertex('sibling')]),
      );
      final read = client.getVertex(
        'read',
        options: const GetOptions(source: ReadSource.serverOnly),
      );
      await h.tick();
      expect(h.remote.calls, 2);
      expect(readCalls, 1);
      authGate.complete();
      await flush();
      expect(
        await h.store.transaction(
          (tx) => tx.replayPausedForAuth(h.identity.stableIdentity),
        ),
        true,
      );
      var installed = false;
      final refresh = client.acceptVerifiedCredentials(
        OfflineVerifiedCredentials(
          binding: h.identity,
          install: () {
            installed = true;
            authenticated = true;
          },
        ),
      );
      await flush();
      expect(installed, false);
      readGate.complete();
      sendGate.complete();
      expect((await read).confirmed.state, ConfirmedState.unknown);
      await refresh.timeout(const Duration(seconds: 2));
      await h.tick();
      expect(installed, true);
      expect(await h.rows(), isEmpty);
    },
  );

  test(
    'C3 expired/reused events and lifecycle resume do not clear policy holds',
    () async {
      final h = Harness();
      addTearDown(h.close);
      h.remote.after = (_) async => throw const OfflineRemoteFailure(
        OfflineRemoteErrorKind.permanent,
        'fixture',
      );
      final client = await h.open();
      await client.mutations.submit(
        MutationRequest.putVertices([vertex('policy')]),
      );
      await h.tick();
      expect(
        await h.store.transaction(
          (tx) => tx.replayPausedForPolicy(h.identity.stableIdentity),
        ),
        true,
      );
      var installed = 0;
      final expired = OfflineVerifiedCredentials(
        binding: binding(expires: h.clock.now),
        install: () {
          installed++;
        },
      );
      expect(await client.acceptVerifiedCredentials(expired), false);
      final valid = OfflineVerifiedCredentials(
        binding: h.identity,
        install: () {
          installed++;
        },
      );
      expect(await client.acceptVerifiedCredentials(valid), true);
      expect(await client.acceptVerifiedCredentials(valid), false);
      await client.resume();
      await h.tick(5000);
      expect(installed, 1);
      expect(h.remote.calls, 1);
      expect(
        await h.store.transaction(
          (tx) => tx.replayPausedForPolicy(h.identity.stableIdentity),
        ),
        true,
      );
      expect(
        await client.acceptVerifiedCredentials(
          OfflineVerifiedCredentials(
            binding: binding(account: 'different'),
            install: () {
              installed++;
            },
          ),
        ),
        false,
      );
      expect(h.client.isClosed, true);
      expect(installed, 1);
    },
  );

  test(
    'C3 reopen cannot clear persisted auth hold with a fresh process counter',
    () async {
      final h = Harness();
      addTearDown(h.close);
      h.remote.after = (_) async => throw const OfflineRemoteFailure(
        OfflineRemoteErrorKind.unauthenticated,
        'fixture',
      );
      final first = await h.open();
      await first.mutations.submit(
        MutationRequest.putVertices([vertex('auth')]),
      );
      await h.tick();
      await first.close();
      final client = LanternClient.connect(
        endpoint,
        allowInsecure: true,
        transport: FakeTransportBuilder().build(),
      );
      final remote = MutationRemote();
      final reopened = await OfflineLanternSession.openStore(
        client: client,
        storeKey: h.store,
        createStore: () async => h.store,
        closeStore: (_) async {},
        binding: h.identity,
        durability: AdmissionDurability.memory,
        clock: h.clock.call,
        schedule: h.clock.schedule,
        mutationRemote: remote,
        receiptRemote: h.receipt,
      );
      addTearDown(reopened.close);
      await h.tick(5000);
      expect(remote.calls, 0);
      await reopened.resume();
      await h.tick();
      expect(remote.calls, 0);
      await reopened.acceptVerifiedCredentials(
        OfflineVerifiedCredentials(binding: h.identity, install: () {}),
      );
      await h.tick();
      expect(remote.calls, 1);
      expect(await h.rows(), isEmpty);
    },
  );

  test(
    'S4 real facade cacheOnly has zero RPC, watch resumes automatically, one-shot leaves no poll',
    () async {
      var reads = 0, tokens = 0;
      final h = Harness(
        token: () {
          tokens++;
          return null;
        },
        transport: FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (request, _) {
                reads++;
                return graph.GetVerticesResponse(missing: request.keys);
              },
            )
            .build(),
      );
      addTearDown(h.close);
      final client = await h.open();
      await client.getVertex(
        'cache',
        options: const GetOptions(source: ReadSource.cacheOnly),
      );
      expect(reads, 0);
      expect(tokens, 0);
      final values = <LanternSnapshot<Vertex>>[];
      final sub = client
          .watchVertex(
            'watch',
            options: const WatchOptions(pollInterval: Duration(seconds: 1)),
          )
          .listen(values.add);
      await flush();
      await h.tick();
      expect(reads, 1);
      await client.suspend();
      await h.tick(5000);
      expect(reads, 1);
      await client.resume();
      await h.tick();
      expect(reads, 2);
      await sub.cancel();
      final one = client.getVertex('once');
      await h.tick();
      await one;
      final before = reads;
      await h.tick(5000);
      expect(reads, before);
      expect(h.clock.active, 0);
    },
  );

  test(
    'S4 existing auth-paused watch resumes only after verified refresh',
    () async {
      var allowed = false, calls = 0;
      final h = Harness(
        transport: FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (request, _) {
                calls++;
                if (!allowed) {
                  throw connect.ConnectException(
                    connect.Code.unauthenticated,
                    'fixture',
                  );
                }
                return graph.GetVerticesResponse(missing: request.keys);
              },
            )
            .build(),
      );
      addTearDown(h.close);
      final client = await h.open();
      final events = <LanternSnapshot<Vertex>>[];
      final watch = client.watchVertex('v').listen(events.add);
      addTearDown(watch.cancel);
      await flush();
      await h.tick();
      expect(calls, 1);
      await h.tick(10000);
      expect(calls, 1);
      allowed = true;
      await client.acceptVerifiedCredentials(
        OfflineVerifiedCredentials(binding: h.identity, install: () {}),
      );
      await h.tick();
      expect(calls, 2);
      expect(
        events.last.confirmed.cause,
        isNot(ObservationCause.unauthenticated),
      );
    },
  );

  test(
    'T16 transfer and restore pauses keep expiry/status local; hints cannot bypass',
    () async {
      final h = Harness();
      addTearDown(h.close);
      final client = await h.open(transferAllowed: false);
      final health = <LanternSyncStatus>[];
      final status = client.syncStatus.listen(health.add);
      addTearDown(status.cancel);
      await client.mutations.submit(
        MutationRequest.putVertices([vertex('live')]),
      );
      await client.mutations.submit(
        MutationRequest.putVertices([
          vertex(
            'expired',
            expires: h.clock.now.add(const Duration(seconds: 1)),
          ),
        ]),
      );
      client.notifyReachability();
      await h.tick(2000);
      expect(h.remote.calls, 0);
      expect(health.last.pause, SyncPause.transfer);
      expect((await h.rows()).map((r) => r.intent.key.vertexKey), ['live']);
      await client.setTransferAllowed(true);
      await h.tick();
      expect(h.remote.calls, 1);
      final restored = Harness();
      addTearDown(restored.close);
      final held = await restored.open(restoreHeld: true);
      final restoredHealth = <LanternSyncStatus>[];
      final sub = held.syncStatus.listen(restoredHealth.add);
      addTearDown(sub.cancel);
      await held.mutations.submit(MutationRequest.deleteVertices(['v']));
      await held.acceptVerifiedCredentials(
        OfflineVerifiedCredentials(binding: restored.identity, install: () {}),
      );
      held.notifyReachability();
      await restored.tick(10000);
      expect(restored.receipt.receiptPrepareCalls, 0);
      expect(restoredHealth.last.pause, SyncPause.restore);
    },
  );

  test(
    'T16 shared Retry-After prevents Get/hint storms and persists next attempt',
    () async {
      var wires = 0, reads = 0;
      final h = Harness(
        transport: FakeTransportBuilder()
            .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
              LanternService.putVertices,
              (_, _) {
                if (++wires == 1) {
                  throw connect.ConnectException(
                    connect.Code.resourceExhausted,
                    'fixture',
                    metadata: connect.Headers()..add('retry-after', '5'),
                  );
                }
                return graph.PutVerticesResponse(
                  outcomes: [graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE],
                );
              },
            )
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (request, _) {
                reads++;
                return graph.GetVerticesResponse(missing: request.keys);
              },
            )
            .build(),
      );
      addTearDown(h.close);
      final client = await h.open(parentRemote: true);
      final health = <LanternSyncStatus>[];
      final sub = client.syncStatus.listen(health.add);
      addTearDown(sub.cancel);
      await client.mutations.submit(MutationRequest.putVertices([vertex('v')]));
      await h.tick();
      final row = (await h.rows()).single;
      expect(row.nextAttemptAt, h.clock.now.add(const Duration(seconds: 5)));
      for (var i = 0; i < 20; i++) {
        client.notifyReachability();
        await client.getVertex('other');
      }
      await h.tick(1000);
      expect(wires, 1);
      expect(reads, 0);
      expect(health.last.reachability, SyncReachability.backoff);
      expect(health.last.activeMutations, 1);
      await h.tick(4000);
      expect(wires, 2);
      expect(await h.rows(), isEmpty);
    },
  );

  test(
    'automatic workers keep same-key FIFO while another identity progresses',
    () async {
      final h = Harness();
      addTearDown(h.close);
      final hold = Completer<void>();
      var held = false;
      h.remote.before = (r) async {
        if (r.intent.key.vertexKey == 'same' && !held) {
          held = true;
          await hold.future;
        }
      };
      final client = await h.open();
      await client.mutations.submit(
        MutationRequest.putVertices([vertex('same')]),
      );
      await client.mutations.submit(
        MutationRequest.putVertices([vertex('same')]),
      );
      await client.mutations.submit(
        MutationRequest.putVertices([vertex('other')]),
      );
      await h.tick();
      await h.tick(1000);
      expect(h.remote.sent.map((r) => r.intent.key.vertexKey), ['other']);
      hold.complete();
      await flush();
      await h.tick(1000);
      expect(h.remote.sent.map((r) => r.intent.key.vertexKey), [
        'other',
        'same',
        'same',
      ]);
      expect(await h.rows(), isEmpty);
    },
  );

  test(
    'S4 permission-denied watch stops polling and verified auth does not clear policy',
    () async {
      var reads = 0;
      final h = Harness(
        transport: FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (_, _) {
                reads++;
                throw connect.ConnectException(
                  connect.Code.permissionDenied,
                  'fixture',
                );
              },
            )
            .build(),
      );
      addTearDown(h.close);
      final client = await h.open();
      final values = <LanternSnapshot<Vertex>>[];
      final sub = client
          .watchVertex(
            'denied',
            options: const WatchOptions(pollInterval: Duration(seconds: 1)),
          )
          .listen(values.add);
      await flush();
      await h.tick();
      expect(reads, 1);
      await h.tick(5000);
      expect(reads, 1);
      await client.acceptVerifiedCredentials(
        OfflineVerifiedCredentials(binding: h.identity, install: () {}),
      );
      await h.tick(5000);
      expect(reads, 1);
      expect(values.last.confirmed.state, ConfirmedState.unknown);
      expect(values.last.confirmed.cause, ObservationCause.permissionDenied);
      await sub.cancel();
    },
  );

  test(
    'C4 live settlement survives confirmation failure and blocks later dispatch',
    () async {
      final clock = Clock(), raw = MemoryOfflineStore();
      final store = ConfirmationFaultStore(raw);
      final remote = MutationRemote();
      final transport = LanternClient.connect(
        endpoint,
        allowInsecure: true,
        transport: FakeTransportBuilder().build(),
      );
      final client = await OfflineLanternSession.openStore(
        client: transport,
        storeKey: raw,
        createStore: () async => store,
        closeStore: (_) async {},
        binding: binding(),
        durability: AdmissionDurability.memory,
        clock: clock.call,
        schedule: clock.schedule,
        mutationRemote: remote,
        receiptRemote: FakeOfflineRemote(),
      );
      addTearDown(client.close);
      final ticket = await client.mutations.submit(
        MutationRequest.putVertices([vertex('v')]),
        options: const MutationOptions(wait: WaitOptions()),
      );
      remote.after = (_) async {
        store.fail = true;
      };
      await clock.advance(const Duration(milliseconds: 50));
      final observed = await ticket.completion!;
      expect(
        observed.reply!.items.single,
        isA<MutationKnownEffect<PutOutcome>>(),
      );
      final retained = (await raw.transaction(
        (tx) => tx.outbox(binding().stableIdentity),
      )).single;
      expect(retained.mayHaveDispatched, true);
      await expectLater(
        client.mutations.submit(
          MutationRequest.putVertices([vertex('blocked')]),
        ),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.storageBlocked,
          ),
        ),
      );
      await clock.advance(const Duration(seconds: 5));
      expect(remote.calls, 1);
    },
  );

  for (final missing in [false, true]) {
    test(
      'S4 watched ${missing ? 'Missing' : 'Vertex'} expires on one local alarm before long poll',
      () async {
        final h = Harness();
        addTearDown(h.close);
        final client = await h.open();
        final now = h.clock.now,
            expiry = h.clock.now.add(const Duration(seconds: 2));
        await h.store.transaction((tx) async {
          await tx.putCache(
            h.identity.stableIdentity,
            missing
                ? OfflineCacheRecord.missing(
                    partitionId: h.identity.stableIdentity,
                    generation: 0,
                    key: OfflineEntityKey.vertex('cached'),
                    validatedAt: now,
                    lastAccessAt: now,
                    missingUntil: expiry,
                  )
                : OfflineCacheRecord.value(
                    partitionId: h.identity.stableIdentity,
                    generation: 0,
                    key: OfflineEntityKey.vertex('cached'),
                    validatedAt: now,
                    lastAccessAt: now,
                    entity: Vertex(
                      key: 'cached',
                      value: VertexValue.string('value'),
                      expiration: expiry,
                    ),
                  ),
          );
        });
        final values = <LanternSnapshot<Vertex>>[];
        final sub = client
            .watchVertex(
              'cached',
              options: const WatchOptions(
                source: ReadSource.cacheOnly,
                pollInterval: Duration(hours: 1),
              ),
            )
            .listen(values.add);
        await flush();
        await h.tick();
        expect(
          values.last.confirmed.state,
          missing ? ConfirmedState.missing : ConfirmedState.present,
        );
        await h.tick(2000);
        expect(values.last.confirmed.state, ConfirmedState.expired);
        expect(h.remote.calls, 0);
        expect(h.clock.active, lessThanOrEqualTo(1));
        await sub.cancel();
        expect(h.clock.active, 0);
      },
    );
  }

  for (final lifecycle in [false, true]) {
    test(
      'R1 install hold survives ${lifecycle ? 'lifecycle' : 'transfer'} resume and joins old publication',
      () async {
        final h = Harness();
        addTearDown(h.close);
        final oldReply = Completer<void>(),
            install = Completer<void>(),
            installing = Completer<void>();
        var installed = false;
        final dispatchedWithNewCredentials = <bool>[];
        h.remote.after = (_) {
          dispatchedWithNewCredentials.add(installed);
          return h.remote.calls == 1 ? oldReply.future : Future.value();
        };
        final client = await h.open();
        final old = await client.mutations.submit(
          MutationRequest.putVertices([vertex('old')]),
        );
        await h.tick();
        final refresh = client.acceptVerifiedCredentials(
          OfflineVerifiedCredentials(
            binding: h.identity,
            install: () async {
              installing.complete();
              await install.future;
              installed = true;
            },
          ),
        );
        await flush();
        expect(installing.isCompleted, false);
        oldReply.complete();
        await installing.future;
        expect(
          (await client.mutations.getStatus(
            old.reference.operationId,
          )).items.single.state,
          MutationItemState.stillPending,
        );
        if (lifecycle) {
          await client.suspend();
          await client.resume();
        }
        await client.setTransferAllowed(true);
        await client.mutations.submit(
          MutationRequest.putVertices([vertex('new')]),
        );
        await h.tick();
        expect(h.remote.calls, 1);
        expect((await h.rows()).every((r) => r.leaseOwner == null), true);
        install.complete();
        expect(await refresh, true);
        await h.tick();
        expect(dispatchedWithNewCredentials, [false, true, true]);
        expect(await h.rows(), isEmpty);
      },
    );
  }

  for (final cached in [false, true]) {
    test(
      'R2 serverOnly entity captures hide ${cached ? 'cache' : 'pending Put'} and preserve pending metadata',
      () async {
        final h = Harness();
        addTearDown(h.close);
        final client = await h.open(transferAllowed: false);
        if (cached) {
          await h.store.transaction((tx) async {
            for (final (key, entity) in <(OfflineEntityKey, Object)>[
              (
                OfflineEntityKey.vertex('v'),
                Vertex(
                  key: 'v',
                  value: VertexValue.string('cached'),
                  expiration: null,
                ),
              ),
              (
                OfflineEntityKey.edge('v', 'w'),
                const Edge(tail: 'v', head: 'w', weight: 1, expiration: null),
              ),
            ]) {
              await tx.putCache(
                h.identity.stableIdentity,
                OfflineCacheRecord.value(
                  partitionId: h.identity.stableIdentity,
                  generation: 0,
                  key: key,
                  entity: entity,
                  validatedAt: h.clock.now,
                  lastAccessAt: h.clock.now,
                ),
              );
            }
          });
        } else {
          await client.mutations.submit(
            MutationRequest.putVertices([vertex('v')]),
          );
          await client.mutations.submit(
            MutationRequest.putEdges([
              EdgeInput(tail: 'v', head: 'w', weight: 2),
            ]),
          );
        }
        const options = WatchOptions(source: ReadSource.serverOnly);
        final values = List.generate(4, (_) => <LanternSnapshot<Object>>[]);
        final subscriptions = <StreamSubscription<Object?>>[
          client.watchVertex('v', options: options).listen(values[0].add),
          client
              .watchVertices(['v'], options: options)
              .listen(values[1].addAll),
          client
              .watchEdge(const EdgeRef('v', 'w'), options: options)
              .listen(values[2].add),
          client
              .watchEdges([const EdgeRef('v', 'w')], options: options)
              .listen(values[3].addAll),
        ];
        await flush();
        for (final events in values) {
          expect(events.length, greaterThanOrEqualTo(2));
          for (final event in events) {
            expect(event.view, isNull);
            expect(event.confirmed.state, ConfirmedState.unknown);
            expect(event.confirmed.value, isNull);
            expect(event.projection, PendingProjection.none);
          }
          expect(events.last.pending.count, cached ? 0 : 1);
        }
        for (final sub in subscriptions) {
          await sub.cancel();
        }
        expect(h.remote.calls, 0);
      },
    );
  }

  test(
    'R2 internally interrupted serverOnly Get keeps Unknown over cached value',
    () async {
      final reply = Completer<graph.GetVerticesResponse>();
      final h = Harness(
        transport: FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (_, _) => reply.future,
            )
            .build(),
      );
      addTearDown(h.close);
      final client = await h.open();
      await h.store.transaction(
        (tx) => tx.putCache(
          h.identity.stableIdentity,
          OfflineCacheRecord.value(
            partitionId: h.identity.stableIdentity,
            generation: 0,
            key: OfflineEntityKey.vertex('v'),
            entity: Vertex(
              key: 'v',
              value: VertexValue.string('cached'),
              expiration: null,
            ),
            validatedAt: h.clock.now,
            lastAccessAt: h.clock.now,
          ),
        ),
      );
      final read = client.getVertex(
        'v',
        options: const GetOptions(source: ReadSource.serverOnly),
      );
      await h.tick();
      final stopping = client.suspend();
      final value = await read;
      expect(value.confirmed.state, ConfirmedState.unknown);
      expect(value.view, isNull);
      reply.complete(graph.GetVerticesResponse(missing: ['v']));
      await stopping;
    },
  );

  for (final inFlight in [false, true]) {
    test(
      'R3 ${inFlight ? 'in-flight' : 'queued'} Get cancellation is typed and frees its read slot',
      () async {
        final reply = Completer<graph.GetVerticesResponse>();
        var calls = 0;
        final h = Harness(
          transport: FakeTransportBuilder()
              .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
                LanternService.getVertices,
                (request, _) {
                  calls++;
                  return request.keys.single == 'v'
                      ? reply.future
                      : Future.value(
                          graph.GetVerticesResponse(missing: request.keys),
                        );
                },
              )
              .build(),
        );
        addTearDown(h.close);
        final client = await h.open();
        final token = LanternCancellationToken();
        final read = client.getVertex(
          'v',
          options: GetOptions(cancellation: token),
        );
        final canceled = expectLater(
          read,
          throwsA(isA<LanternCanceledException>()),
        );
        if (inFlight) {
          await h.tick();
        } else {
          await flush();
        }
        expect(calls, inFlight ? 1 : 0);
        token.cancel();
        await canceled;
        reply.complete(graph.GetVerticesResponse(missing: ['v']));
        await flush();
        final next = client.getVertex('next');
        await h.tick();
        expect((await next).confirmed.state, ConfirmedState.missing);
        expect(calls, inFlight ? 2 : 1);
        final local = await client.getVertex(
          'v',
          options: const GetOptions(source: ReadSource.cacheOnly),
        );
        expect(local.confirmed.state, ConfirmedState.unknown);
        await h.tick();
        expect(calls, inFlight ? 2 : 1);
        expect(h.clock.active, 0);
      },
    );
  }
}

final class ConfirmationFaultStore
    implements OfflineStore, OfflineAutomaticStore {
  ConfirmationFaultStore(this.inner);
  final MemoryOfflineStore inner;
  bool fail = false;
  @override
  Object get canonicalOwnerKey => inner.canonicalOwnerKey;
  @override
  Future<OfflineAutomaticOwner> acquireAutomaticOwner({
    required String scope,
    required String bindingFingerprint,
    required String configurationFingerprint,
  }) => inner.acquireAutomaticOwner(
    scope: scope,
    bindingFingerprint: bindingFingerprint,
    configurationFingerprint: configurationFingerprint,
  );
  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction) action,
  ) => inner.transaction((tx) async {
    final result = await action(tx);
    if (fail &&
        (await tx.operations(binding().stableIdentity)).any(
          (op) => op.items.any((i) => i.state == OfflineWriteState.confirmed),
        )) {
      throw StateError('confirmation fixture');
    }
    return result;
  });
  @override
  Stream<OfflineStoreChange> changes(String scope) => inner.changes(scope);
}
