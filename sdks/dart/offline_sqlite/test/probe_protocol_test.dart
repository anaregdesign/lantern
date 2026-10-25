import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';

import '../tool/probe_protocol.dart';

void main() {
  test('lifecycle retains fixed observations without imposing phase order', () {
    var ticks = 0;
    final lifecycle = ProbeLifecycle(
      mode: 'verify',
      scenario: 'schema',
      boundary: 'before',
      elapsed: () => Duration(microseconds: ticks++),
    );
    expect(_phases(lifecycle), ['launch']);
    lifecycle.record(ProbePhase.spawned);
    lifecycle.observeWorker(probePhaseMarker(ProbePhase.entered));
    // schema/before can reach ready while open is still pending. A later
    // verification also opens raw SQLite before opening the production store.
    lifecycle.observeWorker(probePhaseMarker(ProbePhase.opening));
    lifecycle.record(ProbePhase.ready);
    lifecycle.observeWorker(probePhaseMarker(ProbePhase.opening));
    lifecycle.observeWorker(probePhaseMarker(ProbePhase.opened));
    expect(_phases(lifecycle), [
      'launch',
      'spawned',
      'entered',
      'opening',
      'ready',
      'opening',
      'opened',
    ]);
    lifecycle.observeWorker({'event': 'probe_phase', 'phase': 'timeout'});
    lifecycle.observeWorker({'event': 'probe_phase', 'phase': 'private token'});
    lifecycle.observeWorker({
      'event': 'probe_phase',
      'phase': 'opened',
      'path': 'private path',
    });
    expect(_phases(lifecycle).last, 'opened');
    for (var i = 0; i < maxProbePhaseRecords * 3; i++) {
      lifecycle.observeWorker(probePhaseMarker(ProbePhase.opening));
    }
    lifecycle.record(ProbePhase.timeout);
    lifecycle.record(ProbePhase.cleanupKill);
    lifecycle.record(ProbePhase.cleanupExit);
    final snapshot = lifecycle.snapshot();
    expect(snapshot['worker_entry_observed'], isTrue);
    expect(snapshot['truncated'], isTrue);
    expect(_phases(lifecycle), hasLength(maxProbePhaseRecords));
    expect(_phases(lifecycle).sublist(maxProbePhaseRecords - 3), [
      'timeout',
      'cleanupKill',
      'cleanupExit',
    ]);
    expect(jsonEncode(snapshot), isNot(contains('private')));
    expect(
      () => ProbeLifecycle(
        mode: 'private credential',
        scenario: 'schema',
        boundary: 'before',
      ),
      throwsArgumentError,
    );
    expect(() => probePhaseMarker(ProbePhase.timeout), throwsArgumentError);
  });

  test('unterminated launcher progress cannot consume the entry marker', () {
    final lifecycle = ProbeLifecycle(
      mode: 'verify',
      scenario: 'scoped-partial',
      boundary: 'before',
    );
    var invalid = 0;
    final lines = ProbeJsonLines(
      onMessage: lifecycle.observeWorker,
      onInvalid: () => invalid++,
    );
    final output = utf8.encode(
      'Running build hooks...${probePhaseFrame(ProbePhase.entered)}',
    );
    lines.add(output.sublist(0, 5));
    lines.add(output.sublist(5, output.length - 2));
    lines.add(output.sublist(output.length - 2));
    lines.finish();
    expect(invalid, 1);
    expect(_phases(lifecycle), ['launch', 'entered']);
    expect(lifecycle.snapshot()['worker_entry_observed'], isTrue);
    expect(jsonEncode(lifecycle.snapshot()), isNot(contains('Running')));
    expect(jsonEncode(lifecycle.snapshot()), isNot(contains('build hooks')));
  });

  test('hostile fragmented child lines remain bounded and redacted', () {
    final lifecycle = ProbeLifecycle(
      mode: 'verify',
      scenario: 'scoped-partial',
      boundary: 'before',
    );
    var invalid = 0;
    final lines = ProbeJsonLines(
      onMessage: lifecycle.observeWorker,
      onInvalid: () => invalid++,
    );
    final entered = utf8.encode(
      '${jsonEncode(probePhaseMarker(ProbePhase.entered))}\n',
    );
    lines.add(entered.sublist(0, 7));
    lines.add(entered.sublist(7));
    lines.add(utf8.encode('private secret' * maxProbeLineBytes));
    lines.add(
      utf8.encode('${jsonEncode(probePhaseMarker(ProbePhase.closed))}\n'),
    ); // A valid suffix never rescues an oversized line.
    lines.add([255, 10]);
    lines.add(
      utf8.encode(
        '${jsonEncode(probePhaseMarker(ProbePhase.opened))}\n'
        '{"event":"probe_phase","phase":"closed","value":"secret"}\n',
      ),
    );
    lines.add(utf8.encode('private unterminated' * maxProbeLineBytes));
    lines.finish();
    expect(invalid, 3);
    expect(_phases(lifecycle), ['launch', 'entered', 'opened']);
    expect(jsonEncode(lifecycle.snapshot()), isNot(contains('secret')));
    expect(jsonEncode(lifecycle.snapshot()), isNot(contains('private')));
  });

  test(
    'single verification Object is capped without accepting truncation',
    () async {
      expect(
        await collectProbeMessage(
          Stream.fromIterable([
            utf8.encode('  {"event":'),
            utf8.encode('"verified"}\n'),
          ]),
        ),
        {'event': 'verified'},
      );
      for (final raw in [
        '{"event":"verified"}${' ' * maxProbeLineBytes}',
        '{"event":"verified"}\n{"event":"verified"}',
        'private secret',
      ]) {
        expect(
          await collectProbeMessage(Stream.value(utf8.encode(raw))),
          isNull,
        );
      }
    },
  );

  test('deadline and bounded cleanup retain their actual outcomes', () async {
    final lifecycle = ProbeLifecycle(
      mode: 'verify',
      scenario: 'scoped-partial',
      boundary: 'before',
    );
    await expectLater(
      waitForProbe(Completer<void>().future, Duration.zero, lifecycle),
      throwsA(isA<TimeoutException>()),
    );
    var killed = 0;
    await cleanupProbe(
      exited: false,
      kill: () {
        killed++;
        return true;
      },
      exitCode: Future.value(-9),
      timeout: Duration.zero,
      lifecycle: lifecycle,
    );
    expect(killed, 1);
    expect(_phases(lifecycle), [
      'launch',
      'timeout',
      'cleanupKill',
      'cleanupExit',
    ]);
    await cleanupProbe(
      exited: true,
      kill: () {
        killed++;
        return true;
      },
      exitCode: Completer<int>().future,
      timeout: Duration.zero,
      lifecycle: lifecycle,
    );
    expect(killed, 1);
    final stuckCleanup = ProbeLifecycle(
      mode: 'crash',
      scenario: 'schema',
      boundary: 'before',
    );
    await expectLater(
      cleanupProbe(
        exited: false,
        kill: () => false,
        exitCode: Completer<int>().future,
        timeout: Duration.zero,
        lifecycle: stuckCleanup,
      ),
      throwsA(isA<TimeoutException>()),
    );
    expect(_phases(stuckCleanup), ['launch', 'cleanupKill', 'timeout']);
    expect(stuckCleanup.snapshot()['worker_entry_observed'], isFalse);
  });

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

List<String> _phases(ProbeLifecycle lifecycle) => [
  for (final record
      in lifecycle.snapshot()['records']! as List<Map<String, Object>>)
    record['phase']! as String,
];
