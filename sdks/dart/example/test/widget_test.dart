import 'dart:typed_data';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_example/main.dart';

void main() {
  test('Put example helpers reject non-live outcomes', () {
    expect(
      () => requireAppliedPut('vertex v', PutOutcome.expired),
      throwsStateError,
    );
    expect(
      () => requireAppliedVertexPuts([
        VertexPutResult(key: 'v', outcome: PutOutcome.superseded),
      ]),
      throwsStateError,
    );
    requireAppliedPut('vertex v', PutOutcome.appliedAndLive);
  });

  testWidgets('missing runtime configuration fails closed with guidance', (
    tester,
  ) async {
    await tester.pumpWidget(const LanternExampleApp(configuration: null));

    expect(find.byKey(const Key('configuration-help')), findsOneWidget);
    expect(find.textContaining('LANTERN_ALLOW_INSECURE'), findsOneWidget);
  });

  test('formats every Vertex oneof without losing type or precision', () {
    final timestamp = DateTime.parse('2026-07-12T01:02:03Z');
    const duration = Duration(seconds: -12, microseconds: -345);

    expect(formatVertexValue(VertexValue.float64(1.25)), 'float64=1.25');
    expect(formatVertexValue(VertexValue.float32(2.5)), 'float32=2.5');
    expect(
      formatVertexValue(VertexValue.int32(-0x80000000)),
      'int32=-2147483648',
    );
    expect(
      formatVertexValue(VertexValue.int64(-0x8000000000000000)),
      'int64=-9223372036854775808',
    );
    expect(
      formatVertexValue(VertexValue.uint32(0xffffffff)),
      'uint32=4294967295',
    );
    expect(
      formatVertexValue(VertexValue.uint64((BigInt.one << 64) - BigInt.one)),
      'uint64=18446744073709551615',
    );
    expect(formatVertexValue(VertexValue.boolean(true)), 'bool=true');
    expect(
      formatVertexValue(VertexValue.string('line\n"quoted"')),
      r'string="line\n\"quoted\""',
    );
    expect(
      formatVertexValue(VertexValue.bytes(Uint8List.fromList([0, 1, 255]))),
      'bytes=base64:AAH/',
    );
    expect(
      formatVertexValue(VertexValue.timestamp(timestamp)),
      'timestamp=2026-07-12T01:02:03.000Z',
    );
    expect(
      formatVertexValue(VertexValue.duration(duration)),
      'duration_us=-12000345',
    );
    expect(formatVertexValue(VertexValue.nil()), 'nil');
    expect(formatVertexValue(VertexValue.unset()), 'unset');
  });
}
