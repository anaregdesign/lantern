// @dart=3.11
// Shared by the Dart and Flutter entry libraries, so no single URI applies.
// ignore: use_string_in_part_of_directives
part of traversal_contract_tests;

// The same finite tests run on snapshots and the actual OS SQLite adapter.
void traversalContract({
  required Future<OfflineStore> Function() create,
  required Future<OfflineStore> Function(OfflineStore) reopen,
}) {
  late TraversalFixture h;
  setUp(() async => h = TraversalFixture(await create()));
  tearDown(() => h.close());

  group('T01 route and source policy', () {
    for (final certified in [false, true]) {
      test('current empty Graph and outage, certified=$certified', () async {
        await h.open(certified: certified);
        final empty = await h.read();
        expect(empty.confirmed.state, ConfirmedState.present);
        expect(empty.confirmed.value!.vertices, isEmpty);
        final calls = h.calls, tokens = h.tokens;
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          certified ? ConfirmedState.present : ConfirmedState.unknown,
        );
        expect(h.calls, calls);
        expect(h.tokens, tokens);
        h.unavailable = true;
        final fallback = await h.read();
        expect(
          fallback.confirmed.state,
          certified ? ConfirmedState.present : ConfirmedState.unknown,
        );
        expect(
          fallback.confirmed.origin,
          certified ? ObservationOrigin.retainedCache : ObservationOrigin.none,
        );
        expect(
          (await h.read(ReadSource.serverOnly)).confirmed.state,
          ConfirmedState.unknown,
        );
        expect(
          () => h.data!.watchIlluminate(
            'seed',
            traversal: const PprOptions(),
            options: const WatchOptions(coverage: WatchCoverage.strictLive),
          ),
          throwsA(
            isA<LanternDataException>().having(
              (e) => e.reason,
              'reason',
              DataFailure.unsupported,
            ),
          ),
        );
      });
    }
    test('cache disabled never inserts reusable results', () async {
      await h.open(cache: false);
      await h.read();
      expect(await h.record(), isNull);
      expect(
        (await h.read(ReadSource.cacheOnly)).confirmed.cause,
        ObservationCause.noCache,
      );
    });
  });

  group('T02 route fences', () {
    for (final boundary in ['local lookup', 'local publication']) {
      test('R2 revocation at $boundary cannot revive retained Graph', () async {
        final inner = h.store;
        final store = TraversalBoundaryStore(inner, h.scope);
        h.store = store;
        await h.open(continues: true);
        await h.read();
        final record = (await h.record())!;
        final calls = h.calls, tokens = h.tokens;
        if (boundary == 'local lookup') {
          store.beforeBegin = () async {
            store.beforeBegin = null;
            h.current = false;
          };
          store.afterCommit = () async {
            store.afterCommit = null;
            h.current = true;
          };
        } else {
          store.afterCommit = () async {
            store.afterCommit = null;
            h.current = false;
          };
        }
        final during = await h.read(ReadSource.cacheOnly);
        final epoch = await inner.transaction(
          (tx) => tx.traversalValidityEpoch(h.scope),
        );
        h.current = true;
        final retry = await h.read(ReadSource.cacheOnly);
        await h.close();
        h.store = await reopen(inner);
        await h.open(continues: true);
        final reopened = await h.read(ReadSource.cacheOnly);
        expect([
          during.confirmed.state,
          retry.confirmed.state,
          reopened.confirmed.state,
        ], List.filled(3, ConfirmedState.unknown));
        expect(epoch, greaterThan(record.validityEpoch));
        expect(h.calls, calls);
        expect(h.tokens, tokens);
      });
    }
    for (final code in [
      connect.Code.unauthenticated,
      connect.Code.permissionDenied,
    ]) {
      test('R1 current $code still pauses its own scope', () async {
        await h.open();
        await h.read();
        final record = (await h.record())!;
        h.beforeResponse = () async {
          throw connect.ConnectException(code, 'finite denied response');
        };
        final denied = await h.read();
        expect(denied.confirmed.state, ConfirmedState.unknown);
        expect(
          denied.confirmed.cause,
          code == connect.Code.unauthenticated
              ? ObservationCause.unauthenticated
              : ObservationCause.permissionDenied,
        );
        await h.store.transaction((tx) async {
          expect(
            await tx.replayPausedForAuth(h.scope),
            code == connect.Code.unauthenticated,
          );
          expect(
            await tx.replayPausedForPolicy(h.scope),
            code == connect.Code.permissionDenied,
          );
          expect(
            await tx.traversalValidityEpoch(h.scope),
            greaterThan(record.validityEpoch),
          );
        });
        h.beforeResponse = null;
        final calls = h.calls, tokens = h.tokens;
        expect((await h.read()).confirmed.state, ConfirmedState.unknown);
        expect(h.calls, calls);
        expect(h.tokens, tokens);
      });
      test('R1 delayed $code cannot pause or invalidate a successor', () async {
        final inner = h.store;
        final store = TraversalBoundaryStore(inner, h.scope);
        h.store = store;
        await h.open();
        Future<Map<String, Object?>> state(OfflineStoreTransaction tx) async =>
            {
              'generation': await tx.generation(h.scope),
              'authority': await tx.authorityBinding(h.scope),
              'authPaused': await tx.replayPausedForAuth(h.scope),
              'policyPaused': await tx.replayPausedForPolicy(h.scope),
              'epoch': await tx.traversalValidityEpoch(h.scope),
            };
        late Map<String, Object?> successor;
        h.beforeResponse = () async {
          store.beforeBegin = () async {
            store.beforeBegin = null;
            await inner.transaction((tx) async {
              await tx.wipePartition(h.scope);
              await tx.bindAuthority(h.scope, 'successor-authority');
              successor = await state(tx);
            });
          };
          throw connect.ConnectException(code, 'finite denied response');
        };
        await expectLater(
          h.read(),
          throwsA(
            isA<LanternDataException>().having(
              (e) => e.reason,
              'reason',
              DataFailure.closed,
            ),
          ),
        );
        expect(await inner.transaction(state), successor);
      });
    }
    for (final boundary in ['token', 'response']) {
      test('revocation at $boundary prevents retention/publication', () async {
        await h.open();
        if (boundary == 'token') {
          h.beforeToken = () async {
            h.current = false;
          };
        } else {
          h.beforeResponse = () async {
            h.current = false;
          };
        }
        final result = await h.read();
        expect(result.confirmed.state, ConfirmedState.unknown);
        expect(await h.record(), isNull);
        expect(h.calls, boundary == 'token' ? 0 : 1);
        h.current =
            true; // A known revoked lifetime cannot be revived by its label.
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.unknown,
        );
      });
    }
    test('a route from another client is rejected', () async {
      await h.open();
      final other = LanternClient.connect(h.endpoint, allowInsecure: true);
      addTearDown(other.close);
      await expectLater(
        OfflineLanternDataClient.open(
          client: other,
          store: h.store,
          binding: h.binding,
          durability: AdmissionDurability.memory,
          readRoute: h.route,
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
    });
  });

  group('T03 cross-owner continuation', () {
    for (final continues in [null, false, true]) {
      test('reopen continuation=$continues keeps original deadlines', () async {
        await h.open();
        await h.read();
        final before = (await h.record())!;
        await h.close();
        h.store = await reopen(h.store);
        h.now = h.now.add(const Duration(seconds: 29));
        await h.open(continues: continues);
        h.unavailable = true;
        final result = await h.read(ReadSource.cacheOnly);
        expect(
          result.confirmed.state,
          continues == true ? ConfirmedState.present : ConfirmedState.unknown,
        );
        if (continues == true) {
          expect(result.confirmed.observedAt, before.observedAt);
        }
        final after = (await h.record())!;
        expect(after.freshUntil, before.freshUntil);
        expect(after.historyUntil, before.historyUntil);
        expect(h.calls, 1);
        expect(h.tokens, 1);
      });
    }
    test('changed authority defeats a true continuation assertion', () async {
      await h.open();
      await h.read();
      await h.close();
      h.authority = 'new';
      await h.open(continues: true);
      expect(
        (await h.read(ReadSource.cacheOnly)).confirmed.state,
        ConfirmedState.unknown,
      );
    });
  });

  group('T04 semantic validity versus availability', () {
    test(
      'read race epoch preserves history; unreturned candidate invalidates it',
      () async {
        await h.open();
        await h.read();
        final epoch = (await h.record())!.validityEpoch;
        await h.store.transaction((tx) => tx.advanceReadEpoch(h.scope));
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.present,
        );
        expect(
          await h.store.transaction((tx) => tx.traversalValidityEpoch(h.scope)),
          epoch,
        );
        await h.store.transaction(
          (tx) => tx.resetScopedChangeCursor(
            h.scope,
            OfflineScopedChangeCursor([1]),
          ),
        );
        await h.read();
        await h.store.transaction(
          (tx) => tx.applyScopedChangeFrame(
            h.scope,
            OfflineScopedChangeFrame(
              keys: [OfflineEntityKey.vertex('unreturned')],
              cursor: OfflineScopedChangeCursor([2]),
            ),
          ),
        );
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.unknown,
        );
      },
    );
    test(
      'pending unsent metadata preserves Graph; possible dispatch invalidates',
      () async {
        await h.open();
        await h.read();
        await h.data!.mutations.submit(
          MutationRequest.putVertices([
            VertexInput(key: 'pending', value: VertexValue.string('local')),
          ]),
        );
        final local = await h.read(ReadSource.cacheOnly);
        expect(local.confirmed.state, ConfirmedState.present);
        expect(local.confirmed.value!.vertices, isEmpty);
        expect(local.pending.count, 1);
        await h.store.transaction(
          (tx) => tx.invalidateMutationCache(
            h.scope,
            OfflineEntityKey.vertex('pending'),
          ),
        );
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.unknown,
        );
      },
    );
    test('cursor-only frame preserves semantic epoch', () async {
      await h.open();
      await h.store.transaction(
        (tx) =>
            tx.resetScopedChangeCursor(h.scope, OfflineScopedChangeCursor([1])),
      );
      await h.read();
      final record = (await h.record())!;
      h.now = h.now.add(const Duration(seconds: 35));
      final before = await h.read(ReadSource.cacheOnly);
      expect(before.confirmed.freshness, ObservationFreshness.historical);
      await h.store.transaction(
        (tx) => tx.applyScopedChangeFrame(
          h.scope,
          OfflineScopedChangeFrame(
            keys: [],
            cursor: OfflineScopedChangeCursor([2]),
          ),
        ),
      );
      final after = await h.read(ReadSource.cacheOnly);
      final retained = (await h.record())!;
      expect(after.confirmed.state, ConfirmedState.present);
      expect(after.confirmed.freshness, ObservationFreshness.historical);
      expect(after.confirmed.observedAt, before.confirmed.observedAt);
      expect(after.observationMode, before.observationMode);
      expect(retained.validityEpoch, record.validityEpoch);
      expect(retained.freshUntil, record.freshUntil);
      expect(retained.historyUntil, record.historyUntil);
      expect(h.calls, 1);
    });
  });

  for (final event in ['empty', 'key', 'prefix', 'reset']) {
    test('T04 legacy store semantic invalidation: $event', () async {
      await h.open();
      await h.read();
      final epoch = (await h.record())!.validityEpoch;
      await h.store.transaction((tx) async {
        if (event == 'reset') {
          await tx.resetChangeCursor(h.scope, OfflineChangeCursor(const {}));
        } else {
          await tx.applyChangeChunk(
            h.scope,
            OfflineChangeChunk(
              origin: '0123456789abcdef0123456789abcdef',
              sequence: BigInt.one,
              chunkIndex: 0,
              isLast: true,
              keys: event == 'key'
                  ? [OfflineEntityKey.vertex('unreturned')]
                  : [],
              vertexPrefixes: event == 'prefix' ? ['unreturned:'] : [],
            ),
          );
        }
      });
      expect(
        await h.store.transaction((tx) => tx.traversalValidityEpoch(h.scope)),
        epoch + (event == 'empty' ? 0 : 1),
      );
      expect(
        (await h.read(ReadSource.cacheOnly)).confirmed.state,
        event == 'empty' ? ConfirmedState.present : ConfirmedState.unknown,
      );
    });
  }

  group('T05 original deadlines and whole Graph expiry', () {
    for (final age in [30, 59, 60]) {
      test('historical boundary $age seconds', () async {
        await h.open();
        await h.read();
        await h.close();
        h.store = await reopen(h.store);
        h.now = h.now.add(Duration(seconds: age));
        await h.open(continues: true);
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          age < 60 ? ConfirmedState.present : ConfirmedState.unknown,
        );
      });
    }
    test('one included Vertex TTL expires all synthetic edges', () async {
      h.graph = wire.Graph(
        vertices: [
          wire.Vertex(
            key: 'seed',
            expiration: Timestamp.fromDateTime(
              h.now.add(const Duration(seconds: 5)),
            ),
          ),
          wire.Vertex(key: 'x'),
        ],
        edges: [wire.Edge(tail: 'seed', head: 'x', weight: .25)],
      );
      await h.open();
      await h.read();
      expect(
        (await h.record())!.historyUntil,
        h.now.add(const Duration(seconds: 5)),
      );
      h.now = h.now.add(const Duration(seconds: 5));
      expect(
        (await h.read(ReadSource.cacheOnly)).confirmed.state,
        ConfirmedState.unknown,
      );
      expect(
        await h.store.transaction(
          (tx) => tx.getCache(h.scope, OfflineEntityKey.edge('seed', 'x')),
        ),
        isNull,
      );
    });
  });

  group('T06 persisted clock rollback', () {
    test(
      '12:00 to 11:00: successful responses never become fallback',
      () async {
        await h.open();
        await h.read();
        final original = (await h.record())!;
        await h.close();
        h.store = await reopen(h.store);
        h.now = h.now.subtract(const Duration(hours: 1));
        for (var turn = 0; turn < 3; turn++) {
          await h.open(continues: true);
          expect((await h.read()).confirmed.state, ConfirmedState.present);
          expect(
            (await h.read(ReadSource.cacheOnly)).confirmed.state,
            ConfirmedState.unknown,
          );
          expect((await h.record())!.ownerTag, original.ownerTag);
          expect(
            await h.store.transaction(
              (tx) => tx.traversalValidityEpoch(h.scope),
            ),
            greaterThan(original.validityEpoch),
          );
          await h.close();
          h.store = await reopen(h.store);
          h.now = h.now.add(const Duration(minutes: 1));
        }
        h.now = original.observedAt;
        await h.open(continues: true);
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.unknown,
        );
        await h.read();
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.present,
        );
      },
    );
  });

  group('T07 mid-attempt clock phases', () {
    test(
      'detected rollback followed by immediate close never revives history',
      () async {
        await h.open();
        await h.read();
        final original = h.now;
        h.now = original.subtract(const Duration(hours: 1));
        h.data!
            .clock(); // Detect and latch, then close before the coalesced task runs.
        await h.close();
        h.store = await reopen(h.store);
        h.now = original;
        await h.open(continues: true);
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.unknown,
        );
      },
    );

    for (final boundary in ['token', 'response']) {
      test('rollback at $boundary never revives an old result', () async {
        await h.open();
        await h.read();
        final high = h.now;
        Future<void> rollback() async {
          h.now = high.subtract(const Duration(hours: 1));
        }

        if (boundary == 'token') {
          h.beforeToken = rollback;
        } else {
          h.beforeResponse = rollback;
        }
        expect((await h.read()).confirmed.state, ConfirmedState.unknown);
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.unknown,
        );
        h.beforeToken = null;
        h.beforeResponse = null;
        h.now = high;
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.unknown,
        );
        await h.read();
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.present,
        );
      });
    }
  });

  for (final boundary in ['before-commit', 'after-commit']) {
    for (final event in ['route', 'rollback']) {
      test('T02/T07 $event at $boundary fences committed response', () async {
        final underlying = h.store;
        final wrapped = TraversalBoundaryStore(underlying, h.scope);
        h.store = wrapped;
        await h.open();
        var injected = false;
        Future<void> change(OfflineStoreTransaction? tx) async {
          final record = tx == null
              ? await underlying.transaction(
                  (tx) => tx.getTraversal(
                    h.scope,
                    LanternTraversalQuery(
                      'seed',
                      traversal: const PprOptions(),
                    ).canonicalKey,
                  ),
                )
              : await tx.getTraversal(
                  h.scope,
                  LanternTraversalQuery(
                    'seed',
                    traversal: const PprOptions(),
                  ).canonicalKey,
                );
          if (injected || record == null) return;
          injected = true;
          if (event == 'route') {
            h.current = false;
          } else {
            h.now = h.now.subtract(const Duration(hours: 1));
          }
        }

        if (boundary == 'before-commit') {
          wrapped.beforeCommit = change;
        } else {
          wrapped.afterCommit = () => change(null);
        }
        expect((await h.read()).confirmed.state, ConfirmedState.unknown);
        expect(injected, isTrue);
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.unknown,
        );
        wrapped.beforeCommit = null;
        wrapped.afterCommit = null;
        await h.close();
        h.store = await reopen(underlying);
        h.current = true;
        h.now = DateTime.utc(2026, 10, 15, 12);
        await h.open(continues: true);
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.unknown,
        );
      });
    }
  }

  group('T08 atomic storage boundaries', () {
    test(
      'result commit failure blocks storage and never becomes a cache miss',
      () async {
        final wrapped = TraversalBoundaryStore(h.store, h.scope);
        h.store = wrapped;
        await h.open();
        wrapped.beforeCommit = (tx) async {
          if (await tx!.getTraversal(
                h.scope,
                LanternTraversalQuery(
                  'seed',
                  traversal: const PprOptions(),
                ).canonicalKey,
              ) !=
              null) {
            throw StateError('injected result commit failure');
          }
        };
        await expectLater(h.read(), throwsStateError);
        wrapped.beforeCommit = null;
        expect(await h.record(), isNull);
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.cause,
          ObservationCause.unavailable,
        );
      },
    );

    test(
      'aborted result/epoch/high-water transaction preserves original bytes',
      () async {
        await h.open();
        await h.read();
        final before = (await h.record())!;
        final bytes = OfflineTraversalCodec.encode(before);
        await expectLater(
          h.store.transaction<void>((tx) async {
            await tx.invalidateTraversals(h.scope);
            await tx.effectiveTime(h.scope, h.now.add(const Duration(days: 1)));
            throw StateError('injected transaction abort');
          }),
          throwsStateError,
        );
        expect(OfflineTraversalCodec.encode((await h.record())!), bytes);
        expect(
          await h.store.transaction((tx) => tx.traversalValidityEpoch(h.scope)),
          before.validityEpoch,
        );
        expect(
          (await h.read(ReadSource.cacheOnly)).confirmed.state,
          ConfirmedState.present,
        );
      },
    );
  });

  group('T09 codec and independent storage bounds', () {
    test(
      'persisted query keys use parent binary32 equivalence and family identity',
      () async {
        await h.open();
        final queries = [
          const PprOptions(restartProbability: 0.1),
          const PprOptions(restartProbability: 0.10000000149011612),
          const PprOptions(restartProbability: 0.10000002),
          const BfsOptions(step: 1, fanOut: 2),
          const LocalCommunityOptions(),
        ];
        final keys = <String>[];
        for (final options in queries) {
          final query = LanternTraversalQuery('seed', traversal: options);
          await h.data!.illuminate('seed', traversal: options);
          final row = await h.store.transaction(
            (tx) => tx.getTraversal(h.scope, query.canonicalKey),
          );
          expect(row!.queryKey, query.canonicalKey);
          keys.add(row.queryKey);
        }
        expect(keys[0], keys[1]);
        expect(keys.toSet(), hasLength(4));
      },
    );
    test(
      'G5 edge member boundary and byte rejection preserve the valid row',
      () async {
        String key(int i) => i.toRadixString(36);
        h.graph = wire.Graph(vertices: [wire.Vertex(key: 'seed')]);
        await h.open();
        await h.read(ReadSource.serverOnly);
        final before = (await h.record())!;
        final bytes = OfflineTraversalCodec.encode(before);
        expect(
          OfflineTraversalCodec.reservedBytes(before),
          lessThan(OfflineTraversalRecord.maxBytes),
        );
        final large = wire.Graph(
          vertices: [for (var i = 0; i < 129; i++) wire.Vertex(key: key(i))],
          edges: [
            for (var tail = 0; tail < 128; tail++)
              for (var head = 0; head < 128; head++)
                wire.Edge(tail: key(tail), head: key(head), weight: 1),
          ],
        );
        OfflineTraversalRecord memberRecord() => OfflineTraversalRecord(
          partitionId: before.partitionId,
          generation: before.generation,
          authorityBinding: before.authorityBinding,
          responderId: before.responderId,
          ownerTag: before.ownerTag,
          queryKey: before.queryKey,
          validityEpoch: before.validityEpoch,
          graph: Graph(
            vertices: [
              for (final v in large.vertices)
                Vertex(key: v.key, value: const NilValue(), expiration: null),
            ],
            edges: [
              for (final e in large.edges)
                Edge(
                  tail: e.tail,
                  head: e.head,
                  weight: e.weight,
                  expiration: null,
                ),
            ],
          ),
          observedAt: before.observedAt,
          freshUntil: before.freshUntil,
          historyUntil: before.historyUntil,
          sourceExpiration: null,
          securityUntil: before.securityUntil,
          lastAccessAt: before.lastAccessAt,
        );
        // Construction isolates membership from strict encoded retention bytes.
        expect(memberRecord().graph.allEdges, hasLength(16384));
        for (final count in [16384, 16385]) {
          if (count == 16385) {
            large.edges.add(wire.Edge(tail: key(128), head: key(0), weight: 1));
            expect(memberRecord, throwsA(isA<OfflineArgumentException>()));
          }
          expect(
            large.edges.map((e) => '${e.tail}/${e.head}').toSet(),
            hasLength(count),
          );
          // Match the parent's protobuf bytes plus per-member envelope bound,
          // so the second rejection isolates its response member limit.
          final parentBytes =
              large.vertices.fold<int>(
                0,
                (n, v) => n + v.writeToBuffer().length + 16,
              ) +
              large.edges.fold<int>(
                0,
                (n, e) => n + e.writeToBuffer().length + 16,
              );
          expect(parentBytes, lessThan(1 << 20));
          h.graph = large;
          await expectLater(
            h.read(ReadSource.serverOnly),
            throwsA(
              isA<LanternDataException>().having(
                (e) => e.reason,
                'reason',
                DataFailure.capacity,
              ),
            ),
          );
          expect(OfflineTraversalCodec.encode((await h.record())!), bytes);
          final cached = await h.read(ReadSource.cacheOnly);
          expect(cached.confirmed.observedAt, before.observedAt);
          expect(cached.confirmed.value!.vertices.keys, ['seed']);
          expect(cached.confirmed.value!.allEdges, isEmpty);
          expect(OfflineTraversalCodec.encode((await h.record())!), bytes);
        }
      },
    );
    for (final size in ['vertices', 'encoded-bytes']) {
      test('result $size capacity fails typed and untruncated', () async {
        h.graph = size == 'vertices'
            ? wire.Graph(
                vertices: List.generate(4097, (i) => wire.Vertex(key: 'v$i')),
              )
            : wire.Graph(
                vertices: [
                  wire.Vertex(key: 'seed', bytes: List.filled(800000, 7)),
                ],
              );
        await h.open();
        await expectLater(
          h.read(),
          throwsA(
            isA<LanternDataException>().having(
              (error) => error.reason,
              'reason',
              DataFailure.capacity,
            ),
          ),
        );
        expect(await h.record(), isNull);
      });
    }
    test(
      'retained codec preserves exact Graph values and synthetic edge TTL',
      () async {
        h.graph = wire.Graph(
          vertices: [
            wire.Vertex(key: 'nil', nil: true),
            wire.Vertex(key: 'unset'),
            wire.Vertex(key: 'bytes', bytes: [0, 255, 7]),
            wire.Vertex(key: 'float', float32: 0.1),
          ],
          edges: [
            wire.Edge(
              tail: 'nil',
              head: 'bytes',
              weight: 0.1,
              expiration: Timestamp.fromDateTime(
                h.now.add(const Duration(seconds: 20)),
              ),
            ),
          ],
        );
        await h.open();
        final server = await h.read();
        final local = await h.read(ReadSource.cacheOnly);
        // A synthetic Graph pair cannot satisfy an exact entity-cache read.
        expect(
          (await h.data!.getEdge(
            const EdgeRef('nil', 'bytes'),
            options: const GetOptions(source: ReadSource.cacheOnly),
          )).confirmed.state,
          ConfirmedState.unknown,
        );
        expect(local.confirmed.value!.vertices['nil']!.value, isA<NilValue>());
        expect(
          local.confirmed.value!.vertices['unset']!.value,
          isA<UnsetValue>(),
        );
        expect(
          (local.confirmed.value!.vertices['bytes']!.value as BytesValue).value,
          [0, 255, 7],
        );
        expect(
          local.confirmed.value!.allEdges.single.weight,
          server.confirmed.value!.allEdges.single.weight,
        );
        expect(
          local.confirmed.value!.allEdges.single.expiration,
          server.confirmed.value!.allEdges.single.expiration,
        );
        final original = OfflineTraversalCodec.encode((await h.record())!);
        expect(
          OfflineTraversalCodec.encode(OfflineTraversalCodec.decode(original)),
          original,
        );
      },
    );

    test(
      'strict result codec rejects duplicate identities, fields and rewritten deadlines',
      () async {
        h.graph = wire.Graph(vertices: [wire.Vertex(key: 'seed')]);
        await h.open();
        await h.read();
        final record = (await h.record())!;
        final bytes = OfflineTraversalCodec.encode(record);
        expect(
          OfflineTraversalCodec.encode(OfflineTraversalCodec.decode(bytes)),
          bytes,
        );
        for (final change in <void Function(Map<String, dynamic>)>[
          (m) => m['surprise'] = true,
          (m) => (m['vertices'] as List).add((m['vertices'] as List).first),
          (m) => m['historyUntil'] = h.now
              .add(const Duration(days: 1))
              .toIso8601String(),
        ]) {
          final m = jsonDecode(bytes) as Map<String, dynamic>;
          change(m);
          expect(
            () => OfflineTraversalCodec.decode(jsonEncode(m)),
            throwsA(isA<OfflineCodecException>()),
          );
        }
      },
    );
    test(
      '33 inactive queries evict one result without touching pending work',
      () async {
        await h.open();
        await h.data!.mutations.submit(
          MutationRequest.putVertices([
            VertexInput(key: 'pending', value: VertexValue.nil()),
          ]),
        );
        final before = await h.store.transaction(
          (tx) async => (await tx.outbox(
            h.scope,
          )).map(OfflineCodec.encodeOutboxRecord).toList(),
        );
        for (var i = 0; i < 33; i++) {
          await h.read(ReadSource.automatic, 'seed-$i');
          h.now = h.now.add(const Duration(milliseconds: 1));
        }
        expect(await h.record(seed: 'seed-0'), isNull);
        expect(await h.record(seed: 'seed-32'), isNotNull);
        expect(
          await h.store.transaction(
            (tx) async => (await tx.outbox(
              h.scope,
            )).map(OfflineCodec.encodeOutboxRecord).toList(),
          ),
          before,
        );
      },
    );
  });
}

final class TraversalFixture {
  TraversalFixture(this.store);
  OfflineStore store;
  DateTime now = DateTime.utc(2026, 10, 15, 12);
  final endpoint = Uri.parse('http://127.0.0.1:6380');
  String authority = 'a';
  LanternSessionBinding get binding => LanternSessionBinding.application(
    serviceOrigin: endpoint,
    securityDomain: 'traversal-test',
    accountIdentity: 'account',
    authorityBinding: authority,
  );
  String get scope => binding.stableIdentity;
  OfflineLanternDataClient? data;
  LanternClient? transport;
  LanternSingleResponderRoute? route;
  int calls = 0, tokens = 0;
  bool current = true, unavailable = false;
  Future<void> Function()? beforeToken, beforeResponse;
  wire.Graph graph = wire.Graph();
  Future<void> open({
    bool certified = true,
    bool cache = true,
    bool? continues,
  }) async {
    transport = LanternClient.connect(
      endpoint,
      allowInsecure: true,
      clock: () => now,
      tokenProvider: () async {
        tokens++;
        await beforeToken?.call();
        return null;
      },
      transport: FakeTransportBuilder()
          .unary<wire.IlluminateRequest, wire.IlluminateResponse>(
            LanternService.illuminate,
            (request, headers) async {
              calls++;
              await beforeResponse?.call();
              if (unavailable) {
                throw connect.ConnectException(
                  connect.Code.unavailable,
                  'fixture outage',
                );
              }
              return wire.IlluminateResponse(graph: graph);
            },
          )
          .build(),
    );
    route = certified
        ? LanternSingleResponderRoute.certified(
            client: transport!,
            responderId: 'deployment-responder',
            isCurrent: () => current,
            continuesRetainedResponder: continues == null
                ? null
                : (_) => continues,
          )
        : null;
    data = await OfflineLanternDataClient.open(
      client: transport!,
      store: store,
      binding: binding,
      durability: AdmissionDurability.memory,
      cacheReads: cache,
      clock: () => now,
      readRoute: route,
    );
  }

  Future<LanternTraversalSnapshot> read([
    ReadSource source = ReadSource.automatic,
    String seed = 'seed',
  ]) => data!.illuminate(
    seed,
    traversal: const PprOptions(),
    options: TraversalReadOptions(source: source),
  );
  Future<OfflineTraversalRecord?> record({String seed = 'seed'}) =>
      store.transaction(
        (tx) => tx.getTraversal(
          scope,
          LanternTraversalQuery(
            seed,
            traversal: const PprOptions(),
          ).canonicalKey,
        ),
      );
  Future<void> close() async {
    await data?.close();
    await transport?.close();
    data = null;
    transport = null;
  }
}

final class TraversalBoundaryStore implements OfflineStore {
  TraversalBoundaryStore(this.inner, this.scope);
  final OfflineStore inner;
  final String scope;
  Future<void> Function()? beforeBegin;
  Future<void> Function(OfflineStoreTransaction?)? beforeCommit;
  Future<void> Function()? afterCommit;
  @override
  Stream<OfflineStoreChange> changes(String id) => inner.changes(id);
  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction) action,
  ) async {
    await beforeBegin?.call();
    final result = await inner.transaction((tx) async {
      final result = await action(tx);
      await beforeCommit?.call(tx);
      return result;
    });
    await afterCommit?.call();
    return result;
  }
}
