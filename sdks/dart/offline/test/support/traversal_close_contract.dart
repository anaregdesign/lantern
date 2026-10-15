// @dart=3.11
// Shared by the Dart and Flutter entry libraries, so no single URI applies.
// ignore: use_string_in_part_of_directives
part of traversal_contract_tests;

void traversalCloseContract({
  required Future<OfflineStore> Function(bool automatic) create,
  required Future<OfflineStore> Function(OfflineStore) reopen,
}) {
  group('T10 close clock contract', () {
    late TraversalFixture h;
    late _CloseStore store;
    final handles = <OfflineLanternSession>[];
    late OfflineClock clock;
    var storeCloses = 0;

    Future<OfflineLanternSession> openHandle() async {
      final handle = await OfflineLanternSession.openStore(
        client: h.transport!,
        storeKey: store.canonicalOwnerKey,
        createStore: () async => store,
        closeStore: (_) async {
          storeCloses++;
        },
        binding: h.binding,
        durability: AdmissionDurability.memory,
        clock: clock,
        readRoute: h.route,
      );
      handles.add(handle);
      return handle;
    }

    Future<void> prepare({bool automatic = false}) async {
      final raw = await create(automatic);
      h = TraversalFixture(raw);
      store = _CloseStore(raw, h.scope);
      h.store = store;
      clock = () => h.now;
      await h.open(continues: true);
      if (automatic) {
        await h.data!.close();
        h.data = null;
      }
    }

    Future<LanternTraversalSnapshot> read(LanternDataClient data) =>
        data.illuminate('seed', traversal: const PprOptions());

    Future<void> settleEvents() async {
      for (var i = 0; i < 4; i++) {
        await Future<void>.delayed(Duration.zero);
      }
    }

    setUp(() {
      handles.clear();
      storeCloses = 0;
    });
    tearDown(() async {
      // Failed-lifetime tests assert retention first. Only the test owner then
      // releases its native fixture, without claiming production close success.
      store.beforeBegin = null;
      store.beforeCommit = null;
      store.afterCommit = null;
      for (final handle in handles) {
        try {
          await handle.close();
        } catch (_) {}
      }
      try {
        await h.data?.close();
      } catch (_) {}
      await h.transport?.close();
      await store.owner?.releaseFixture();
    });

    for (final rollback in [false, true]) {
      test('C1/C6 queued close preserves bytes, rollback=$rollback', () async {
        await prepare();
        await h.data!.mutations.submit(
          MutationRequest.putVertices([
            VertexInput(key: 'pending', value: VertexValue.string('keep')),
          ]),
        );
        await h.read();
        final record = (await h.record())!;
        final bytes = OfflineTraversalCodec.encode(record);
        final pending = await store.inner.transaction(
          (tx) async => (await tx.outbox(
            h.scope,
          )).map(OfflineCodec.encodeOutboxRecord).toList(),
        );
        final high = h.now;
        final calls = h.calls, tokens = h.tokens;
        if (rollback) {
          h.now = high.subtract(const Duration(hours: 1));
          h.data!.clock();
          h.current = false; // Cleanup grants no read-route capability.
        }
        final first = h.data!.close(), repeated = h.data!.close();
        expect(identical(first, repeated), isTrue);
        await first;
        expect(identical(first, h.data!.close()), isTrue);
        expect(h.calls, calls);
        expect(h.tokens, tokens);
        expect(h.transport!.isClosed, isFalse);
        expect(OfflineTraversalCodec.encode((await h.record())!), bytes);
        expect(
          await store.inner.transaction(
            (tx) => tx.traversalValidityEpoch(h.scope),
          ),
          record.validityEpoch + (rollback ? 1 : 0),
        );
        expect(
          await store.inner.transaction(
            (tx) async => (await tx.outbox(
              h.scope,
            )).map(OfflineCodec.encodeOutboxRecord).toList(),
          ),
          pending,
        );
        await h.close();
        h.store = await reopen(store.inner);
        h.now = high;
        h.current = true;
        await h.open(continues: true);
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          rollback ? ConfirmedState.unknown : ConfirmedState.present,
        );
      });
    }

    for (final outcome in ['success', 'canceled', 'newer-revision']) {
      test('C2 running flush is joined: $outcome', () async {
        await prepare();
        await h.read();
        final original = (await h.record())!;
        final entered = Completer<void>(), release = Completer<void>();
        store.beforeCommit = () async {
          store.beforeCommit = null;
          entered.complete();
          await release.future;
          if (outcome == 'canceled') {
            throw const LanternDataException(DataFailure.closed);
          }
        };
        h.now = original.observedAt.subtract(const Duration(hours: 1));
        h.data!.clock();
        await entered.future;
        if (outcome == 'newer-revision') {
          h.now = original.observedAt;
          h.data!.clock();
          h.now = h.now.subtract(const Duration(hours: 1));
          h.data!.clock();
        }
        var done = false;
        final close = h.data!.close().then((_) => done = true);
        await settleEvents();
        expect(done, isFalse);
        release.complete();
        await close;
        expect(store.invalidationAttempts, outcome == 'success' ? 1 : 2);
        expect(
          await store.inner.transaction(
            (tx) => tx.traversalValidityEpoch(h.scope),
          ),
          original.validityEpoch + (outcome == 'newer-revision' ? 2 : 1),
        );
        final attempts = store.invalidationAttempts;
        await settleEvents();
        expect(store.invalidationAttempts, attempts);
      });
    }

    for (final automatic in [false, true]) {
      for (final storageFailure in [false, true]) {
        test(
          'R2 route settlement joins close: automatic=$automatic, failure=$storageFailure',
          () async {
            await prepare(automatic: automatic);
            final LanternDataClient data = automatic
                ? await openHandle()
                : h.data!;
            await read(data);
            final record = (await h.record())!;
            final calls = h.calls, tokens = h.tokens;
            final entered = Completer<void>(), release = Completer<void>();
            addTearDown(() {
              if (!release.isCompleted) release.complete();
            });
            store.beforeBegin = () async {
              store.beforeBegin = null;
              entered.complete();
              await release.future;
              if (storageFailure) throw StateError('failed route settlement');
            };
            h.current = false;
            final result = data
                .illuminate(
                  'seed',
                  traversal: const PprOptions(),
                  options: const TraversalReadOptions(
                    source: ReadSource.cacheOnly,
                  ),
                )
                .then<Object?>((_) => null, onError: (Object error) => error);
            await entered.future;
            var done = false;
            final close = data.close();
            final outcome = close.then<Object?>(
              (_) {
                done = true;
                return null;
              },
              onError: (Object error) {
                done = true;
                return error;
              },
            );
            await settleEvents();
            expect(done, isFalse);
            expect(store.owner?.releases ?? 0, 0);
            release.complete();
            expect(await result, isNotNull);
            final error = await outcome;
            expect(identical(close, data.close()), isTrue);
            expect(h.calls, calls);
            expect(h.tokens, tokens);
            if (storageFailure) {
              expect(
                error,
                isA<LanternDataException>().having(
                  (e) => e.reason,
                  'reason',
                  DataFailure.storageBlocked,
                ),
              );
              expect(storeCloses, 0);
              expect(store.owner?.releases ?? 0, 0);
              expect(
                await store.inner.transaction(
                  (tx) => tx.traversalValidityEpoch(h.scope),
                ),
                record.validityEpoch,
              );
            } else {
              expect(error, isNull);
              expect(storeCloses, automatic ? 1 : 0);
              expect(store.owner?.releases ?? 0, automatic ? 1 : 0);
              h.store = await reopen(store.inner);
              expect(
                await h.store.transaction(
                  (tx) => tx.traversalValidityEpoch(h.scope),
                ),
                greaterThan(record.validityEpoch),
              );
              h.current = true;
              await h.open(continues: true);
              expect(
                (await h.read(ReadSource.cacheOnly)).confirmed.state,
                ConfirmedState.unknown,
              );
            }
          },
        );
      }
      test(
        'C2 running flush storage failure stays failed: automatic=$automatic',
        () async {
          await prepare(automatic: automatic);
          final LanternDataClient data = automatic
              ? await openHandle()
              : h.data!;
          await read(data);
          final epoch = (await h.record())!.validityEpoch;
          final entered = Completer<void>(), release = Completer<void>();
          addTearDown(() {
            if (!release.isCompleted) release.complete();
          });
          store.beforeCommit = () async {
            store.beforeCommit = null;
            entered.complete();
            await release.future;
            throw StateError('failed running flush');
          };
          h.now = h.now.subtract(const Duration(hours: 1));
          if (automatic) {
            await data.mutations.submit(MutationRequest.putVertices([]));
            await settleEvents();
            // Advance the effective scheduling floor after installing the job;
            // pending invalidation remains owed even when raw time catches up.
            h.now = h.now.add(const Duration(hours: 1, seconds: 1));
            await data.mutations.submit(MutationRequest.putVertices([]));
          } else {
            h.data!.clock();
          }
          await entered.future;
          final close = data.close();
          final outcome = close.then<Object?>(
            (_) => null,
            onError: (Object error) => error,
          );
          await settleEvents();
          release.complete();
          expect(
            await outcome,
            isA<LanternDataException>().having(
              (error) => error.reason,
              'reason',
              DataFailure.storageBlocked,
            ),
          );
          expect(store.invalidationAttempts, 1);
          expect(
            await store.inner.transaction(
              (tx) => tx.traversalValidityEpoch(h.scope),
            ),
            epoch,
          );
          expect(identical(close, data.close()), isTrue);
          expect(storeCloses, 0);
          expect(store.owner?.releases ?? 0, 0);
        },
      );
    }

    test('C3 final handle settles debt before one owner release', () async {
      await prepare(automatic: true);
      final one = await openHandle(), two = await openHandle();
      await read(one);
      final original = (await h.record())!;
      await one.close();
      expect(store.owner!.releases, 0);
      expect(storeCloses, 0);
      await two.suspend();
      h.now = h.now.subtract(const Duration(hours: 1));
      // Empty admission samples the shared effective clock without RPC/store work.
      await two.mutations.submit(MutationRequest.putVertices([]));
      final entered = Completer<void>(), release = Completer<void>();
      store.beforeCommit = () async {
        entered.complete();
        await release.future;
      };
      final close = two.close();
      await entered.future;
      expect(store.owner!.releases, 0);
      expect(storeCloses, 0);
      await expectLater(
        openHandle(),
        throwsA(isA<OfflineOwnerConflictException>()),
      );
      release.complete();
      await close;
      expect(store.owner!.releases, 1);
      expect(storeCloses, 1);
      expect(identical(close, two.close()), isTrue);
      h.store = await reopen(store.inner);
      expect(
        await h.store.transaction((tx) => tx.traversalValidityEpoch(h.scope)),
        original.validityEpoch + 1,
      );
      h.now = original.observedAt;
      await h.open(continues: true);
      expect(
        (await h.read(ReadSource.cacheOnly)).confirmed.state,
        ConfirmedState.unknown,
      );
    });

    test('C6 automatic stable close retains history and refcounts', () async {
      await prepare(automatic: true);
      final first = await openHandle(), last = await openHandle();
      await read(first);
      final before = OfflineTraversalCodec.encode((await h.record())!);
      final calls = h.calls, tokens = h.tokens;
      await first.close();
      expect(store.owner!.releases, 0);
      final closing = last.close();
      expect(identical(closing, last.close()), isTrue);
      await closing;
      expect(store.owner!.releases, 1);
      expect(storeCloses, 1);
      expect(store.invalidationAttempts, 0);
      expect(h.calls, calls);
      expect(h.tokens, tokens);
      h.store = await reopen(store.inner);
      expect(OfflineTraversalCodec.encode((await h.record())!), before);
      await h.open(continues: true);
      expect(
        (await h.read(ReadSource.cacheOnly)).confirmed.state,
        ConfirmedState.present,
      );
    });

    for (final automatic in [false, true]) {
      for (final boundary in [
        'before-callback',
        'before-commit',
        'commit-then-error',
      ]) {
        test(
          'C4 failed close is memoized: automatic=$automatic $boundary',
          () async {
            await prepare(automatic: automatic);
            final LanternDataClient data = automatic
                ? await openHandle()
                : h.data!;
            await read(data);
            final original = (await h.record())!;
            if (automatic) await (data as OfflineLanternSession).suspend();
            h.now = h.now.subtract(const Duration(hours: 1));
            if (automatic) {
              await data.mutations.submit(MutationRequest.putVertices([]));
            } else {
              h.data!.clock();
            }
            Future<void> failCommit() async =>
                throw StateError('private injected detail');
            var begins = 0;
            if (boundary == 'before-callback') {
              store.beforeBegin = () async {
                if (++begins == 2) await failCommit();
              };
            } else if (boundary == 'before-commit') {
              store.beforeCommit = failCommit;
            } else {
              store.afterCommit = failCommit;
            }
            final first = data.close(), concurrent = data.close();
            expect(identical(first, concurrent), isTrue);
            final error = await first.then<Object?>(
              (_) => null,
              onError: (Object e) => e,
            );
            expect(
              error,
              isA<LanternDataException>().having(
                (e) => e.reason,
                'reason',
                DataFailure.storageBlocked,
              ),
            );
            expect(
              error.toString(),
              isNot(contains('private injected detail')),
            );
            expect(identical(first, data.close()), isTrue);
            await expectLater(data.close(), throwsA(same(error)));
            expect(
              store.invalidationAttempts,
              boundary == 'before-callback' ? 0 : 1,
            );
            expect(storeCloses, 0);
            expect(store.owner?.releases ?? 0, 0);
            expect(h.transport!.isClosed, isFalse);
            if (automatic) {
              store.owner!.checkCurrent();
              await expectLater(
                openHandle(),
                throwsA(isA<OfflineOwnerConflictException>()),
              );
            }
            expect(
              await store.inner.transaction(
                (tx) => tx.traversalValidityEpoch(h.scope),
              ),
              original.validityEpoch +
                  (boundary == 'commit-then-error' ? 1 : 0),
            );
            await settleEvents();
            expect(
              store.invalidationAttempts,
              boundary == 'before-callback' ? 0 : 1,
            );
          },
        );
      }
    }

    for (final loss in ['owner', 'authority', 'generation']) {
      test('C5 control fence loss while closing: $loss', () async {
        await prepare(automatic: true);
        final handle = await openHandle();
        await read(handle);
        await handle.suspend();
        h.now = h.now.subtract(const Duration(hours: 1));
        await handle.mutations.submit(MutationRequest.putVertices([]));
        final entered = Completer<void>(), release = Completer<void>();
        store.beforeBegin = () async {
          store.beforeBegin = null;
          entered.complete();
          await release.future;
        };
        final closing = handle.close();
        final result = closing.then<Object?>(
          (_) => null,
          onError: (Object e) => e,
        );
        await entered.future;
        if (loss == 'owner') {
          store.owner!.current = false;
        } else {
          await store.inner.transaction((tx) async {
            if (loss == 'authority') {
              await tx.bindAuthority(h.scope, 'successor');
            } else {
              await tx.wipePartition(h.scope);
            }
          });
        }
        final epoch = await store.inner.transaction(
          (tx) => tx.traversalValidityEpoch(h.scope),
        );
        release.complete();
        expect(
          await result,
          anyOf(isA<OfflineDisposedException>(), isA<LanternDataException>()),
        );
        expect(
          await store.inner.transaction(
            (tx) => tx.traversalValidityEpoch(h.scope),
          ),
          epoch,
        );
        expect(store.owner!.releases, 0);
        expect(storeCloses, 0);
        expect(identical(closing, handle.close()), isTrue);
        await expectLater(
          openHandle(),
          throwsA(isA<OfflineOwnerConflictException>()),
        );
      });
    }
  });
}

final class _CloseStore implements OfflineStore, OfflineAutomaticStore {
  _CloseStore(this.inner, this.scope);
  final OfflineStore inner;
  final String scope;
  Future<void> Function()? beforeBegin, beforeCommit, afterCommit;
  int invalidationAttempts = 0;
  _CloseOwner? owner;
  @override
  Object get canonicalOwnerKey =>
      (inner as OfflineAutomaticStore).canonicalOwnerKey;
  @override
  Future<OfflineAutomaticOwner> acquireAutomaticOwner({
    required String scope,
    required String bindingFingerprint,
    required String configurationFingerprint,
  }) async => owner = _CloseOwner(
    await (inner as OfflineAutomaticStore).acquireAutomaticOwner(
      scope: scope,
      bindingFingerprint: bindingFingerprint,
      configurationFingerprint: configurationFingerprint,
    ),
  );
  @override
  Stream<OfflineStoreChange> changes(String id) => inner.changes(id);
  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction) action,
  ) async {
    await beforeBegin?.call();
    var invalidated = false;
    final result = await inner.transaction((tx) async {
      final epoch = await tx.traversalValidityEpoch(scope);
      final value = await action(tx);
      invalidated = epoch != await tx.traversalValidityEpoch(scope);
      if (invalidated) {
        invalidationAttempts++;
        await beforeCommit?.call();
      }
      return value;
    });
    if (invalidated) await afterCommit?.call();
    return result;
  }
}

final class _CloseOwner implements OfflineAutomaticOwner {
  _CloseOwner(this.inner);
  final OfflineAutomaticOwner inner;
  int releases = 0;
  bool current = true;
  @override
  int get epoch => inner.epoch;
  @override
  void checkCurrent() {
    if (!current) throw const OfflineDisposedException();
    inner.checkCurrent();
  }

  @override
  Future<void> release() async {
    releases++;
    await inner.release();
  }

  Future<void> releaseFixture() => inner.release();
}
