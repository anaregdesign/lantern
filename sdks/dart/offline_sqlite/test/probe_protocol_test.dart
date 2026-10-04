import 'package:flutter_test/flutter_test.dart';

import '../tool/probe_protocol.dart';

void main() {
  test('child failure exposes only fixed stage and category', () {
    final marker = probeFailureMarker(
      ProbeStage.prepared,
      StateError('secret record content'),
    );
    expect(marker, {
      'event': 'failed',
      'stage': 'prepared',
      'code': 'contract',
    });
    expect(parseProbeFailure(marker), 'worker:prepared:contract');
    expect(isProbeFailureCode('worker:prepared:contract'), isTrue);
  });

  test('untrusted child fields and exception strings are never forwarded', () {
    for (final marker in <Map<String, Object?>>[
      {'event': 'failed', 'stage': 'private value', 'code': 'contract'},
      {'event': 'failed', 'stage': 'prepared', 'code': 'private token'},
      {
        'event': 'failed',
        'stage': 'prepared',
        'code': 'contract',
        'message': 'private key',
      },
    ]) {
      expect(parseProbeFailure(marker), isNull);
    }
    expect(isProbeFailureCode('worker:prepared:private token'), isFalse);
    expect(isProbeFailureCode('worker:prepared:contract:private key'), isFalse);
    expect(probeFailureMarker(ProbeStage.open, Exception('private value')), {
      'event': 'failed',
      'stage': 'open',
      'code': 'unexpected',
    });
  });
}
