import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:lantern_example/app_data_session.dart';

import 'app_data_test_support.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test(
    'I6 failed initialization retains both startup and retirement failures',
    () async {
      final startupError = StateError('credential installation failed');
      final closeError = StateError('transport retirement failed');
      final client = LanternClient.connect(
        Uri.parse('https://example.test'),
        onClose: () async {
          throw closeError;
        },
      );
      final binding = LanternSessionBinding.application(
        serviceOrigin: client.endpoint,
        securityDomain: 'failed-init',
        accountIdentity: 'test',
        authorityBinding: 'a',
      );
      final factory = AppDataSessionFactory(
        client: client,
        binding: binding,
        profile: AppDataProfile.memory,
        legacyPartition: 'unused',
      );
      final credentials = OfflineVerifiedCredentials(
        binding: binding,
        install: () {
          throw startupError;
        },
      );
      final result = factory.open(freshCredentials: credentials);
      expect(
        identical(result, factory.open(freshCredentials: credentials)),
        true,
      );
      await expectLater(
        result,
        throwsA(
          isA<AppDataStartupFailure>()
              .having(
                (e) => e.startupError,
                'original error',
                same(startupError),
              )
              .having(
                (e) => e.retirementError,
                'retirement error',
                same(closeError),
              ),
        ),
      );
      await expectLater(client.close(), throwsA(same(closeError)));
    },
  );
  test(
    'I4 SQLite reopen preserves auth pause; one verified event resumes original work',
    () async {
      final h = AppHarness(AppDataProfile.durable);
      await h.open();
      addTearDown(h.close);
      await h.owner.suspend();
      final ticket = await h.data.mutations.submit(
        MutationRequest.putVertices([
          VertexInput(
            key: 'app:paused',
            value: const StringValue('retained'),
            expiresIn: const Duration(minutes: 5),
          ),
        ]),
      );
      final location = await h.factory.resolveLocation!();
      final store = await SqliteOfflineStore.open(
        path: location.path,
        databaseFactory: h.factory.databaseFactory,
      );
      await store.transaction(
        (tx) => tx.setReplayPausedForAuth(
          h.binding.stableIdentity,
          true,
          credentialGeneration: 7,
        ),
      );
      await store.close();
      await h.owner.close();

      Future<AppDataSessionFactory> replacement() async {
        final client = LanternClient.connect(
          h.client.endpoint,
          transport: h.wire,
          clock: h.clock.call,
        );
        return AppDataSessionFactory(
          client: client,
          binding: h.binding,
          profile: h.profile,
          legacyPartition: 'fixture',
          databaseFactory: h.factory.databaseFactory,
          clock: h.clock.call,
          schedule: h.clock.schedule,
          resolveLocation: () async => location,
        );
      }

      final restoredFactory = await replacement();
      final restored = await restoredFactory.open();
      await h.clock.advance(const Duration(seconds: 10));
      await restored.suspend();
      await restored.resume();
      await h.clock.advance(const Duration(seconds: 10));
      expect(
        h.wire.calls,
        isEmpty,
        reason: 'Restore and lifecycle resume are not credential verification',
      );
      await expectLater(
        (restored.data! as OfflineLanternSession).inspectLegacyOperation(
          'old-operation',
        ),
        throwsA(isA<OfflineWorkInterruptedException>()),
      );
      expect(
        (await restored.data!.mutations.getStatus(
          ticket.reference.operationId,
        )).terminal,
        false,
      );
      await restored.close();

      var installations = 0;
      final verified = await replacement();
      final event = OfflineVerifiedCredentials(
        binding: h.binding,
        install: () => installations++,
      );
      final first = verified.open(freshCredentials: event);
      expect(identical(first, verified.open(freshCredentials: event)), true);
      final ready = await first;
      addTearDown(ready.close);
      final completed = await h.clock.settle(
        ready.data!.mutations.reobserve(ticket.reference),
      );
      expect(completed.retention, MutationRetention.retained);
      expect(h.wire.vertices['app:paused']?['string'], 'retained');
      expect(installations, 1);
      expect(
        await (ready.data! as OfflineLanternSession).acceptVerifiedCredentials(
          event,
        ),
        false,
      );
      expect(installations, 1);
    },
  );
  for (final profile in AppDataProfile.values) {
    test(
      'G6 equal-value pending and settled metadata: ${profile.name}',
      () async {
        final h = AppHarness(profile);
        await h.open();
        addTearDown(h.close);
        const key = 'app:equal';
        h.wire.vertices[key] = {'key': key, 'string': 'X'};
        final events = <LanternSnapshot<Vertex>>[];
        final watch = h.data.watchVertex(key).listen(events.add);
        addTearDown(watch.cancel);
        final traversals = <LanternTraversalSnapshot>[];
        final traversalWatch = h.data
            .watchIlluminate(key, traversal: const PprOptions())
            .listen(traversals.add);
        addTearDown(traversalWatch.cancel);
        Future<void> until(bool Function() condition) async {
          for (var i = 0; i < 200 && !condition(); i++) {
            await h.clock.advance(const Duration(milliseconds: 25));
          }
          expect(condition(), true, reason: 'bounded metadata transition');
        }

        bool isX(LanternSnapshot<Vertex> value) =>
            value.view?.value is StringValue &&
            (value.view!.value as StringValue).value == 'X';
        await until(() => events.any((e) => isX(e) && e.pending.count == 0));
        await until(
          () => traversals.any(
            (e) => e.confirmed.state == ConfirmedState.present,
          ),
        );
        final initial = events.lastWhere(isX);
        final hold = Completer<void>();
        h.wire.holdWrite = hold;
        addTearDown(() {
          if (!hold.isCompleted) hold.complete();
        });
        var completed = false;
        final writing = h.data
            .putVertex(VertexInput(key: key, value: const StringValue('X')))
            .then((value) {
              completed = true;
              return value;
            });
        await until(() => events.any((e) => isX(e) && e.pending.count == 1));
        final pending = events.lastWhere((e) => isX(e) && e.pending.count == 1);
        expect(completed, false);
        expect(pending.sequence, greaterThan(initial.sequence));
        hold.complete();
        await h.clock.settle(writing);
        await until(
          () => events.any(
            (e) =>
                isX(e) && e.pending.count == 0 && e.sequence > pending.sequence,
          ),
        );
        final settled = events.lastWhere((e) => isX(e) && e.pending.count == 0);
        expect(settled.sequence, greaterThan(pending.sequence));
        expect(
          events.map((e) => e.sequence),
          orderedEquals(events.map((e) => e.sequence).toList()..sort()),
        );
        expect(
          traversals.map((e) => e.sequence),
          orderedEquals(traversals.map((e) => e.sequence).toList()..sort()),
        );
        await watch.cancel();
        await traversalWatch.cancel();
        if (profile == AppDataProfile.direct ||
            profile == AppDataProfile.durableWithoutReadCache) {
          final calls = h.wire.calls.length;
          expect(
            (await h.data.getVertex(
              key,
              options: const GetOptions(source: ReadSource.cacheOnly),
            )).confirmed.state,
            ConfirmedState.unknown,
          );
          expect(h.wire.calls.length, calls);
        }
      },
    );
    test(
      'I1 common consumer ${profile.name}: CRUD/traversal/watch/duplicate results',
      () async {
        final h = AppHarness(profile);
        await h.open();
        addTearDown(h.close);
        final data = h.data;
        await expectLater(
          h.factory.open(
            freshCredentials: OfflineVerifiedCredentials(
              binding: h.binding,
              install: () {},
            ),
          ),
          throwsA(isA<OfflineOwnerConflictException>()),
        );
        final put = await h.clock.settle(
          data.putVertices([
            VertexInput(key: 'app:a', value: const StringValue('A')),
            VertexInput(key: 'app:b', value: const StringValue('B')),
          ]),
        );
        expect(
          put.items.map((r) => (r as MutationKnownEffect<PutOutcome>).effect),
          [PutOutcome.appliedAndLive, PutOutcome.appliedAndLive],
        );
        final reads = await h.clock.settle(
          data.getVertices(['app:a', 'app:a', 'app:b']),
        );
        expect(reads.map((s) => (s.view!.value as StringValue).value), [
          'A',
          'A',
          'B',
        ]);
        await h.clock.settle(
          data.putEdge(EdgeInput(tail: 'app:a', head: 'app:b', weight: 2)),
        );
        final add = await h.clock.settle(
          data.addEdge(EdgeInput(tail: 'app:a', head: 'app:b', weight: 0.5)),
        );
        expect((add as MutationKnownEffect<double>).effect, 2.5);
        final traversal = await h.clock.settle(
          data.illuminate(
            'app:a',
            traversal: const BfsOptions(step: 1, fanOut: 10),
          ),
        );
        expect(traversal.confirmed.value!.allEdges.single.weight, 2.5);
        final watched = await h.clock.settle(
          data
              .watchVertex('app:a')
              .firstWhere((v) => v.confirmed.state == ConfirmedState.present),
        );
        expect((watched.view!.value as StringValue).value, 'A');
        if (profile == AppDataProfile.durableWithoutReadCache ||
            profile == AppDataProfile.direct) {
          expect(
            (await data.getVertex(
              'app:a',
              options: const GetOptions(source: ReadSource.cacheOnly),
            )).confirmed.state,
            ConfirmedState.unknown,
          );
          expect(
            (await data.illuminate(
              'app:a',
              traversal: const BfsOptions(step: 1, fanOut: 10),
              options: const TraversalReadOptions(source: ReadSource.cacheOnly),
            )).confirmed.state,
            ConfirmedState.unknown,
          );
        }
        final deleted = await h.clock.settle(
          data.deleteVertices(['app:a', 'app:a']),
        );
        expect(
          deleted.items.map((r) => (r as MutationKnownEffect<bool>).effect),
          [true, false],
        );
        Future<void>? notificationClose;
        final retired = h.owner.retired.listen((_) {
          expect(h.owner.isClosed, true);
          notificationClose = h.owner.close();
        });
        final closing = h.owner.close();
        expect(identical(closing, notificationClose), true);
        expect(identical(closing, h.owner.close()), true);
        await closing;
        await retired.cancel();
        expect(h.closes, 1);
        expect(h.clock.alarms.where((a) => !a.canceled), isEmpty);
      },
    );

    test(
      'I1/I7 ${profile.name} admission and transport-only recovery',
      () async {
        final h = AppHarness(profile);
        await h.open();
        addTearDown(h.close);
        h.wire.available = false;
        final ticket = await h.data.mutations.submit(
          MutationRequest.putVertices([
            VertexInput(
              key: 'app:a',
              value: const StringValue('offline'),
              expiresIn: const Duration(minutes: 5),
            ),
          ]),
        );
        expect(
          ticket.durability,
          profile == AppDataProfile.direct || profile == AppDataProfile.memory
              ? AdmissionDurability.memory
              : AdmissionDurability.durable,
        );
        final ordinary = h.data.putVertex(
          VertexInput(key: 'app:b', value: const StringValue('ordinary')),
        );
        var ordinaryDone = false;
        Object? ordinaryError;
        ordinary.then<void>(
          (_) {
            ordinaryDone = true;
          },
          onError: (Object e) {
            ordinaryDone = true;
            ordinaryError = e;
          },
        );
        await h.clock.advance(const Duration(seconds: 1));
        expect(h.wire.blocked, greaterThan(0));
        if (profile == AppDataProfile.direct) {
          expect(ordinaryDone, true);
          expect(ordinaryError, isNotNull);
          final before = h.wire.calls.length;
          h.wire.available = true;
          await h.clock.advance(const Duration(seconds: 40));
          expect(h.wire.calls.length, before);
          expect(h.wire.vertices, isEmpty);
        } else {
          expect(ordinaryDone, false);
          expect(
            (await h.data.mutations.getStatus(
              ticket.reference.operationId,
            )).terminal,
            false,
          );
          h.wire.available = true; // The only application change.
          await h.clock.settle(ordinary);
          final result = await h.clock.settle(
            h.data.mutations.reobserve(ticket.reference),
          );
          expect(result.retention, MutationRetention.retained);
          expect(ordinaryError, isNull);
          expect(h.wire.vertices['app:a']?['string'], 'offline');
          final writes = h.wire.bodies
              .where((e) => e.$1 == 'PutVertices')
              .expand((e) => e.$2['vertices'] as List)
              .where((v) => v['key'] == 'app:a');
          expect(writes.map((v) => v['expiration']).toSet(), hasLength(1));
        }
      },
    );
  }
}
