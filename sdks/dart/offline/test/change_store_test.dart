import 'dart:convert';

import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

void main() {
  test(
    'resident identities round-trip without values or delimiter ambiguity',
    () {
      for (final key in <OfflineEntityKey>[
        const OfflineEntityKey.vertex('a:\u{1F600}'),
        const OfflineEntityKey.edge('a:b', 'c\u0000d'),
      ]) {
        expect(OfflineEntityKey.fromCanonical(key.canonical), key);
      }
      expect(
        () => OfflineEntityKey.fromCanonical('v:1:\u00E9'),
        throwsA(isA<OfflineCodecException>()),
      );
    },
  );
  const origin = '00000000000000000000000000000001';
  test('reference store preserves atomic CDC state across snapshot reopen', () {
    return runChangeStoreConformanceSuite(
      InMemoryOfflineStore.new,
      reopen: (store) async => InMemoryOfflineStore.fromSnapshot(
        await (store as InMemoryOfflineStore).exportSnapshot(),
      ),
    );
  });

  test('cursor codec preserves uint64 and rejects noncanonical metadata', () {
    final maximum = (BigInt.one << 64) - BigInt.one;
    final cursor = OfflineChangeCursor({origin: maximum});
    expect(
      OfflineChangeCursor.fromJson(
        jsonDecode(jsonEncode(cursor.toJson())),
      ).sequences[origin],
      maximum,
    );
    for (final encoded in ['01', '-1', '18446744073709551616', '1.0']) {
      expect(
        () => OfflineChangeCursor.fromJson({origin: encoded}),
        throwsA(isA<OfflineCodecException>()),
      );
    }
    expect(
      () => OfflineChangeCursor({'not-an-origin': BigInt.zero}),
      throwsA(isA<OfflineArgumentException>()),
    );
  });

  test('progress refuses interleaving and skips durable duplicate chunks', () {
    final first = OfflineChangeChunk(
      origin: origin,
      sequence: BigInt.one,
      chunkIndex: 0,
      isLast: false,
    );
    final progress = OfflineChangeProgress(
      completedSequence: BigInt.zero,
    ).accept(first)!;
    expect(progress.accept(first), isNull);
    expect(
      () => progress.accept(
        OfflineChangeChunk(
          origin: origin,
          sequence: BigInt.two,
          chunkIndex: 0,
          isLast: true,
        ),
      ),
      throwsA(isA<OfflineChangeGapException>()),
    );
  });

  test('v5 snapshots gain empty cursors without changing auth pause', () async {
    final original = InMemoryOfflineStore();
    await original.transaction(
      (transaction) => transaction.setReplayPausedForAuth('p', true),
    );
    final decoded =
        jsonDecode(await original.exportSnapshot()) as Map<String, dynamic>;
    decoded['schema'] = 5;
    for (final partition in decoded['partitions'] as List<dynamic>) {
      (partition as Map<String, dynamic>)
        ..remove('changeProgress')
        ..remove('changeEpoch')
        ..remove('unknownResidents');
    }
    final restored = InMemoryOfflineStore.fromSnapshot(jsonEncode(decoded));
    expect(
      await restored.transaction((t) => t.replayPausedForAuth('p')),
      isTrue,
    );
    expect(
      (await restored.transaction((t) => t.changeCursor('p'))).sequences,
      isEmpty,
    );
  });

  test(
    'v6 snapshots retain CDC progress and gain empty recovery state',
    () async {
      const origin = '00000000000000000000000000000001';
      final original = InMemoryOfflineStore();
      await original.transaction(
        (transaction) => transaction.applyChangeChunk(
          'p',
          OfflineChangeChunk(
            origin: origin,
            sequence: BigInt.one,
            chunkIndex: 0,
            isLast: true,
          ),
        ),
      );
      final decoded =
          jsonDecode(await original.exportSnapshot()) as Map<String, dynamic>;
      decoded['schema'] = 6;
      for (final partition in decoded['partitions'] as List<dynamic>) {
        (partition as Map<String, dynamic>)
          ..remove('changeEpoch')
          ..remove('unknownResidents');
      }
      final restored = InMemoryOfflineStore.fromSnapshot(jsonEncode(decoded));
      expect(
        (await restored.transaction(
          (t) => t.changeCursor('p'),
        )).sequences[origin],
        BigInt.one,
      );
      expect(await restored.transaction((t) => t.changeEpoch('p')), 0);
      expect(
        await restored.transaction((t) => t.unknownResidents('p', limit: 1)),
        isEmpty,
      );
    },
  );
}
