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
    expect(lifecycle.snapshot()['kills'], {
      'worker': 'not_requested',
      'workerCleanup': 'not_requested',
      'directCleanup': 'accepted',
    });
    expect(lifecycle.snapshot()['direct_exit'], {
      'state': 'observed',
      'code': -9,
      'category': 'nonzero',
    });
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
    expect(
      (stuckCleanup.snapshot()['kills']! as Map)['directCleanup'],
      'refused',
    );
    expect(stuckCleanup.snapshot()['direct_exit'], {'state': 'incomplete'});
  });

  test(
    'failure cleanup collects a late entry without changing timeout FAIL',
    () async {
      final lifecycle = _lifecycle();
      final child = _FakeProbeChild();
      final message = ProbeMessageBuffer();
      final stdout = ProbeCollector(
        child.stdout.stream,
        onData: message.add,
        onDone: message.finish,
      );
      final lines = ProbeJsonLines(
        onMessage: lifecycle.observeWorker,
        onInvalid: () {},
      );
      final stderr = ProbeCollector<Object?>(
        child.stderr.stream,
        onData: lines.add,
        onDone: () {
          lines.finish();
          return null;
        },
      );
      lifecycle.collect(stdout: stdout, stderr: stderr);
      await expectLater(
        withProbeCleanup(
          body: () =>
              waitForProbe(Completer<int>().future, Duration.zero, lifecycle),
          cleanup: () => cleanupProbe(
            exited: false,
            kill: () {
              child.kills++;
              child.stderr.add(
                utf8.encode(probePhaseFrame(ProbePhase.entered)),
              );
              child.stdout.add(utf8.encode('{"event":"verified"}'));
              unawaited(child.stdout.close());
              unawaited(child.stderr.close());
              child.exitCode.complete(-9);
              return true;
            },
            exitCode: child.exitCode.future,
            timeout: Duration.zero,
            lifecycle: lifecycle,
          ),
        ),
        throwsA(isA<TimeoutException>()),
      );
      expect(child.kills, 1);
      expect(lifecycle.snapshot()['worker_entry_observed'], isTrue);
      expect(
        _phases(lifecycle),
        containsAllInOrder(['timeout', 'cleanupKill']),
      );
      expect(_phases(lifecycle), contains('entered'));
      expect(_phases(lifecycle), contains('cleanupExit'));
      expect(stdout.value, {'event': 'verified'});
      expect(stdout.snapshot(), {
        'completion': 'complete',
        'error': 'none',
        'cancellation': 'not_requested',
      });
      expect(stderr.qualified, isTrue);
      expect(lifecycle.snapshot()['worker_pid_relation'], 'unobserved');
      expect(lifecycle.snapshot()['direct_exit'], {
        'state': 'observed',
        'code': -9,
        'category': 'nonzero',
      });
    },
  );

  test(
    'unterminated pipes and cancellation share one exhausted window',
    () async {
      final lifecycle = _lifecycle();
      lifecycle.observeExit(0);
      final neverCancelled = Completer<void>();
      final stdoutStream = StreamController<List<int>>(
        onCancel: () => neverCancelled.future,
      );
      final stderrStream = StreamController<List<int>>();
      final stdout = ProbeCollector<Object?>(
        stdoutStream.stream,
        onData: (_) {},
        onDone: () => null,
      );
      final stderr = ProbeCollector<Object?>(
        stderrStream.stream,
        onData: (_) {},
        onDone: () => null,
      );
      lifecycle.collect(stdout: stdout, stderr: stderr);
      var killed = 0;
      await expectLater(
        withProbeCleanup(
          body: () async => 0,
          cleanup: () => cleanupProbe(
            exited: true,
            kill: () {
              killed++;
              return true;
            },
            exitCode: Completer<int>().future,
            timeout: Duration.zero,
            lifecycle: lifecycle,
          ),
        ),
        throwsA(isA<TimeoutException>()),
      );
      expect(killed, 0);
      expect(stdout.snapshot(), {
        'completion': 'incomplete',
        'error': 'none',
        'cancellation': 'incomplete',
      });
      expect(stderr.snapshot()['completion'], 'incomplete');
      expect(lifecycle.snapshot()['direct_exit'], {
        'state': 'observed',
        'code': 0,
        'category': 'zero',
      });
      // A cancellation error after the deadline is consumed, never unhandled.
      neverCancelled.completeError(StateError('private cancel token'));
      await Future<void>.delayed(Duration.zero);
      expect(stdout.snapshot()['cancellation'], 'error');
      expect(jsonEncode(lifecycle.snapshot()), isNot(contains('private')));
      await stdoutStream.close();
      await stderrStream.close();
    },
  );

  test('pipe and consumer errors are fixed, drained and fail closed', () async {
    final lifecycle = _lifecycle();
    lifecycle.observeExit(0);
    final child = _FakeProbeChild();
    final stdout = ProbeCollector<Object?>(
      child.stdout.stream,
      onData: (_) => throw StateError('private value'),
      onDone: () => null,
    );
    final stderr = ProbeCollector<Object?>(
      child.stderr.stream,
      onData: (_) {},
      onDone: () => null,
    );
    lifecycle.collect(stdout: stdout, stderr: stderr);
    child.stdout.add([1]);
    child.stderr.addError(StateError('private token'));
    unawaited(child.stdout.close());
    unawaited(child.stderr.close());
    await expectLater(
      cleanupProbe(
        exited: true,
        kill: () => throw StateError('must not kill'),
        exitCode: Completer<int>().future,
        timeout: Duration.zero,
        lifecycle: lifecycle,
      ),
      throwsA(
        isA<StateError>().having((error) => error.message, 'code', 'collector'),
      ),
    );
    expect(stdout.snapshot()['completion'], 'complete');
    expect(stdout.snapshot()['error'], 'consumer');
    expect(stderr.snapshot()['completion'], 'complete');
    expect(stderr.snapshot()['error'], 'stream');
    expect(jsonEncode(lifecycle.snapshot()), isNot(contains('private')));
  });

  test('primary error and stack survive a separate cleanup failure', () async {
    final primary = TimeoutException('private primary details');
    final primaryStack = StackTrace.fromString('private original stack');
    Object? observed;
    StackTrace? observedStack;
    try {
      await withProbeCleanup(
        body: () => Future<int>.error(primary, primaryStack),
        cleanup: () async => throw StateError('private cleanup details'),
      );
    } on Object catch (error, stack) {
      observed = error;
      observedStack = stack;
    }
    expect(observed, same(primary));
    expect(observedStack.toString(), primaryStack.toString());
    await expectLater(
      withProbeCleanup(
        body: () async => 1,
        cleanup: () async => throw StateError('cleanup'),
      ),
      throwsA(
        isA<StateError>().having((error) => error.message, 'code', 'cleanup'),
      ),
    );
  });

  test(
    'collected verification rejections precede secondary cleanup failure',
    () async {
      for (final (code, message, expected) in [
        (
          1,
          {'event': 'failed', 'stage': 'prepared', 'code': 'contract'},
          'worker:prepared:contract',
        ),
        (1, {'event': 'verified'}, 'verification'),
        (0, {'event': 'other'}, 'verification'),
        (0, {'event': 'verified'}, 'cleanup'),
      ]) {
        final lifecycle = _lifecycle();
        final messageBuffer = ProbeMessageBuffer();
        final stdout = ProbeCollector(
          Stream.value(utf8.encode(jsonEncode(message))),
          onData: messageBuffer.add,
          onDone: messageBuffer.finish,
        );
        final stderr = ProbeCollector<Object?>(
          const Stream.empty(),
          onData: (_) {},
          onDone: () => null,
        );
        lifecycle.collect(stdout: stdout, stderr: stderr);
        var validated = 0;
        await expectLater(
          withProbeCleanup(
            body: () async {
              lifecycle.observeExit(code);
              return code;
            },
            cleanup: () async {
              await cleanupProbe(
                exited: true,
                kill: () => throw StateError('must not kill'),
                exitCode: Completer<int>().future,
                timeout: Duration.zero,
                lifecycle: lifecycle,
              );
              throw StateError('cleanup');
            },
            validate: () {
              validated++;
              validateProbeVerification(code, stdout);
            },
          ),
          throwsA(
            isA<StateError>().having(
              (error) => error.message,
              'code',
              expected,
            ),
          ),
        );
        expect(validated, 1);
        expect(stdout.qualified, isTrue);
      }
    },
  );

  test(
    'missing or errored stdout cannot replace the cleanup verdict',
    () async {
      for (final streamError in [false, true]) {
        final lifecycle = _lifecycle();
        final output = StreamController<List<int>>();
        final messageBuffer = ProbeMessageBuffer();
        final stdout = ProbeCollector(
          output.stream,
          onData: messageBuffer.add,
          onDone: messageBuffer.finish,
        );
        final stderr = ProbeCollector<Object?>(
          const Stream.empty(),
          onData: (_) {},
          onDone: () => null,
        );
        lifecycle.collect(stdout: stdout, stderr: stderr);
        output.add(
          utf8.encode(
            '{"event":"failed","stage":"prepared","code":"contract"}',
          ),
        );
        if (streamError) {
          output.addError(StateError('private stream error'));
          unawaited(output.close());
        }
        await expectLater(
          withProbeCleanup(
            body: () async {
              lifecycle.observeExit(1);
              return 1;
            },
            cleanup: () => cleanupProbe(
              exited: true,
              kill: () => throw StateError('must not kill'),
              exitCode: Completer<int>().future,
              timeout: Duration.zero,
              lifecycle: lifecycle,
            ),
            validate: () => validateProbeVerification(1, stdout),
          ),
          streamError
              ? throwsA(
                  isA<StateError>().having(
                    (error) => error.message,
                    'code',
                    'collector',
                  ),
                )
              : throwsA(isA<TimeoutException>()),
        );
        expect(stdout.qualified, isFalse);
        expect(jsonEncode(lifecycle.snapshot()), isNot(contains('private')));
        if (!streamError) await output.close();
      }
    },
  );

  test('signal outcomes and PID equality use only already observed facts', () {
    final lifecycle = _lifecycle();
    expect(lifecycle.snapshot()['worker_pid_relation'], 'unobserved');
    lifecycle.observeWorkerPid(sameAsDirect: true);
    expect(lifecycle.snapshot()['worker_pid_relation'], 'same_as_direct');
    lifecycle.observeWorkerPid(sameAsDirect: false);
    expect(
      lifecycle.snapshot()['worker_pid_relation'],
      'different_from_direct',
    );
    var signals = 0;
    expect(
      lifecycle.observeKill(ProbeKill.worker, () {
        signals++;
        return false;
      }),
      isFalse,
    );
    expect(
      lifecycle.observeKill(ProbeKill.workerCleanup, () {
        signals++;
        return true;
      }),
      isTrue,
    );
    expect(
      () => lifecycle.observeKill(ProbeKill.directCleanup, () {
        signals++;
        throw StateError('private error');
      }),
      throwsStateError,
    );
    expect(signals, 3);
    expect(lifecycle.snapshot()['kills'], {
      'worker': 'refused',
      'workerCleanup': 'accepted',
      'directCleanup': 'error',
    });
    expect(jsonEncode(lifecycle.snapshot()), isNot(contains('private')));
  });

  test('direct exit errors still drain both pipes within cleanup', () async {
    final lifecycle = _lifecycle();
    final child = _FakeProbeChild();
    final stdout = ProbeCollector<Object?>(
      child.stdout.stream,
      onData: (_) {},
      onDone: () => null,
    );
    final stderr = ProbeCollector<Object?>(
      child.stderr.stream,
      onData: (_) {},
      onDone: () => null,
    );
    lifecycle.collect(stdout: stdout, stderr: stderr);
    await expectLater(
      cleanupProbe(
        exited: false,
        kill: () {
          child.kills++;
          unawaited(child.stdout.close());
          unawaited(child.stderr.close());
          return false;
        },
        exitCode: Future.error(StateError('private exit error')),
        timeout: Duration.zero,
        lifecycle: lifecycle,
      ),
      throwsA(
        isA<StateError>().having((error) => error.message, 'code', 'cleanup'),
      ),
    );
    expect(child.kills, 1);
    expect(stdout.qualified, isTrue);
    expect(stderr.qualified, isTrue);
    expect(lifecycle.snapshot()['direct_exit'], {'state': 'error'});
    expect(jsonEncode(lifecycle.snapshot()), isNot(contains('private')));
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

ProbeLifecycle _lifecycle() => ProbeLifecycle(
  mode: 'verify',
  scenario: 'cursor-final',
  boundary: 'before',
);

final class _FakeProbeChild {
  final stdout = StreamController<List<int>>();
  final stderr = StreamController<List<int>>();
  final exitCode = Completer<int>();
  var kills = 0;
}

List<String> _phases(ProbeLifecycle lifecycle) => [
  for (final record
      in lifecycle.snapshot()['records']! as List<Map<String, Object>>)
    record['phase']! as String,
];
