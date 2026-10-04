import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

void main() {
  test(
    'opaque frame state is atomic across snapshot reopen',
    () => runScopedChangeStoreConformanceSuite(
      InMemoryOfflineStore.new,
      reopen: (store) async => InMemoryOfflineStore.fromSnapshot(
        await (store as InMemoryOfflineStore).exportSnapshot(),
      ),
    ),
  );
  test('cursor bytes have defensive ownership and bounded canonical codec', () {
    final input = [1, 255, 0];
    final cursor = OfflineScopedChangeCursor(input);
    input[0] = 9;
    final output = cursor.toBytes();
    output[0] = 8;
    expect(cursor.toBytes(), [1, 255, 0]);
    expect(OfflineScopedChangeCursor.fromJson(cursor.toJson()).toBytes(), [
      1,
      255,
      0,
    ]);
    for (final value in [null, '', '!!!!', 'AQ', 'AQ==\n']) {
      expect(
        () => OfflineScopedChangeCursor.fromJson(value),
        throwsA(isA<OfflineCodecException>()),
      );
    }
    for (final value in [
      <int>[],
      [-1],
      [256],
      List.filled(8193, 0),
    ]) {
      expect(
        () => OfflineScopedChangeCursor(value),
        throwsA(isA<OfflineArgumentException>()),
      );
    }
    expect(cursor.toString(), isNot(contains(cursor.toJson())));
  });
  test('frames reject empty, oversized and malformed identity projections', () {
    expect(
      () => OfflineScopedChangeFrame(),
      throwsA(isA<OfflineArgumentException>()),
    );
    expect(
      () => OfflineScopedChangeFrame(keys: const [OfflineEntityKey.vertex('')]),
      throwsA(isA<OfflineArgumentException>()),
    );
    expect(
      () => OfflineScopedChangeFrame(
        keys: List.filled(1025, const OfflineEntityKey.vertex('key')),
      ),
      throwsA(isA<OfflineCapacityException>()),
    );
    expect(
      () => OfflineScopedChangeFrame(
        bootstrap: true,
        keys: const [OfflineEntityKey.vertex('key')],
        cursor: OfflineScopedChangeCursor([1]),
      ),
      throwsA(isA<OfflineArgumentException>()),
    );
  });
}
