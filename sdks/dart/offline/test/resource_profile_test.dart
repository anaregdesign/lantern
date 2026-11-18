import 'package:test/test.dart';

import '../tool/performance_probe.dart' as probe;
import '../tool/resource_profile.dart';

void main() {
  test('bounded memory resource and replay contracts', () async {
    final result = await probe.referenceStoreContracts(
      ResourceProfile.unitContracts,
    );
    expect(result['confirmedCount'], 33);
    expect(result['sampleCount'], 1);
    final automatic = result['automatic']! as Map<String, Object?>;
    expect(automatic['items'], 11);
    expect(automatic['traversalSamples'], 3);
  });

  test(
    'qualification defaults to full; only explicit parent omits envelopes',
    () {
      expect(ResourceProfile.parse([]), ResourceProfile.full);
      for (final profile in [
        ResourceProfile.full,
        ResourceProfile.parentContracts,
      ]) {
        final arguments = [
          '--profile',
          profile.label,
          '--output',
          'result.json',
        ];
        expect(ResourceProfile.parse(arguments), profile);
        expect(arguments, ['--output', 'result.json']);
        expect(profile.envelope(true), isTrue);
        expect(
          profile.envelope(false),
          profile == ResourceProfile.parentContracts,
        );
      }
    },
  );

  test('invalid, repeated, or diagnostic contract profiles fail closed', () {
    for (final arguments in [
      ['--profile'],
      ['--profile', ''],
      ['--profile', 'diagnostic'],
      ['--profile', 'unit-contracts'],
      ['--profile', 'parent-contracts', '--profile', 'full'],
      ['--profile', 'parent-contracts', '--diagnostic', 'events.jsonl'],
      ['--diagnostic', 'events.jsonl', '--profile', 'parent-contracts'],
    ]) {
      expect(() => ResourceProfile.parse(arguments), throwsFormatException);
    }
    final diagnostic = ['--diagnostic', 'events.jsonl'];
    expect(ResourceProfile.parse(diagnostic), ResourceProfile.full);
    expect(diagnostic, ['--diagnostic', 'events.jsonl']);
  });
}
