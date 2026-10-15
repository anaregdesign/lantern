// @dart=3.11
// Shared by the Dart and Flutter entry libraries, so no single URI applies.
// ignore: use_string_in_part_of_directives
part of traversal_contract_tests;

void traversalLimitsContract(
  Future<OfflineStore> Function(OfflineStoreLimits) create,
) {
  final at = DateTime.utc(2026, 10, 15, 12);
  OfflineTraversalRecord record(String scope, int index, {int bytes = 0}) =>
      OfflineTraversalRecord(
        partitionId: scope,
        generation: 0,
        authorityBinding: 'a',
        responderId: 'r',
        ownerTag: '0123456789abcdef0123456789abcdef',
        queryKey: LanternTraversalQuery(
          'q$index',
          traversal: const PprOptions(),
        ).canonicalKey,
        validityEpoch: 0,
        graph: Graph(
          edges: const [],
          vertices: [
            Vertex(
              key: 'v',
              value: VertexValue.string('x' * bytes),
              expiration: null,
            ),
          ],
        ),
        observedAt: at,
        freshUntil: at.add(const Duration(seconds: 30)),
        historyUntil: at.add(const Duration(seconds: 60)),
        sourceExpiration: null,
        securityUntil: null,
        lastAccessAt: at.add(Duration(milliseconds: index)),
      );
  Future<void> put(OfflineStoreTransaction tx, OfflineTraversalRecord r) async {
    await tx.bindAuthority(r.partitionId, r.authorityBinding);
    await tx.effectiveTime(r.partitionId, r.lastAccessAt);
    await tx.putTraversal(r.partitionId, r);
  }

  test('T09 global 128 result bound evicts LRU across scopes', () async {
    final store = await create(const OfflineStoreLimits());
    final rows = [for (var i = 0; i < 129; i++) record('scope-${i ~/ 32}', i)];
    await store.transaction((tx) async {
      for (final row in rows) {
        await put(tx, row);
      }
    });
    await store.transaction((tx) async {
      expect(
        await tx.getTraversal(rows.first.partitionId, rows.first.queryKey),
        isNull,
      );
      for (final row in rows.skip(1)) {
        expect(await tx.getTraversal(row.partitionId, row.queryKey), isNotNull);
      }
    });
  });

  test(
    'T09 independent byte bounds and LRU touch preserve original deadlines',
    () async {
      final store = await create(
        const OfflineStoreLimits(
          maxTraversalBytes: 4096,
          maxTraversalBytesPerPartition: 2048,
        ),
      );
      final a = record('a', 0, bytes: 1000), b = record('a', 1, bytes: 1000);
      final c = record('b', 2, bytes: 1000), d = record('c', 3, bytes: 1000);
      for (final row in [a, b, c, d]) {
        expect(
          OfflineTraversalCodec.reservedBytes(row),
          inInclusiveRange(1400, 2048),
        );
      }
      await store.transaction((tx) async {
        await put(tx, a);
        await put(tx, b);
      });
      expect(
        await store.transaction((tx) => tx.getTraversal('a', a.queryKey)),
        isNull,
      );
      await store.transaction((tx) => put(tx, c));
      final touched = at.add(const Duration(seconds: 5));
      await store.transaction((tx) async {
        await tx.effectiveTime('a', touched);
        await tx.touchTraversal('a', b.queryKey, touched);
        await put(tx, d);
      });
      expect(
        await store.transaction((tx) => tx.getTraversal('b', c.queryKey)),
        isNull,
      );
      final kept = (await store.transaction(
        (tx) => tx.getTraversal('a', b.queryKey),
      ))!;
      expect(kept.observedAt, b.observedAt);
      expect(kept.freshUntil, b.freshUntil);
      expect(kept.historyUntil, b.historyUntil);
      final before = OfflineTraversalCodec.encode(kept);
      await expectLater(
        store.transaction((tx) => put(tx, record('a', 4, bytes: 3000))),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        OfflineTraversalCodec.encode(
          (await store.transaction((tx) => tx.getTraversal('a', b.queryKey)))!,
        ),
        before,
      );
      expect(
        await store.transaction((tx) => tx.getTraversal('c', d.queryKey)),
        isNotNull,
      );
    },
  );
}
