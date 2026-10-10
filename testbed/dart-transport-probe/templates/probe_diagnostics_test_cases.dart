import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:test/test.dart';

import 'probe_diagnostics.dart';

void diagnosticsTests({
  required String transport,
  required String Function(Object) errorCode,
  required Object deadlineError,
}) {
  late List<String> lines;
  late ProbeDiagnostics diagnostics;
  List<Map<String, dynamic>> events() => lines
      .map(
        (line) =>
            jsonDecode(line.substring('LANTERN_PROBE_EVENT '.length))
                as Map<String, dynamic>,
      )
      .toList();

  setUp(() {
    lines = <String>[];
    diagnostics = ProbeDiagnostics(
      transport: transport,
      errorCode: errorCode,
      writeLine: lines.add,
    );
  });

  test(
    'default events use stdout despite Flutter print interception',
    () async {
      await IOOverrides.runZoned(
        () => runZoned(
          () async {
            final nativeDiagnostics = ProbeDiagnostics(
              transport: transport,
              errorCode: errorCode,
            );
            await nativeDiagnostics.run(
              () => nativeDiagnostics.scenario('marker', () async {}),
            );
          },
          zoneSpecification: ZoneSpecification(
            print: (_, _, _, _) => throw StateError('print was sent to syslog'),
          ),
        ),
        stdout: () => _DiagnosticStdout(lines),
      );
      expect(events().last['kind'], 'result');
      expect(events().last['state'], 'success');
    },
  );

  for (final scenario in ['plaintext', 'trusted_tls', 'marker']) {
    for (final method in [
      'PutVertex',
      'GetVertex',
      'BackupSnapshot',
      'ChannelShutdown',
    ]) {
      test('$scenario $method deadline remains a terminal failure', () async {
        await diagnostics.run(
          () => diagnostics.scenario(scenario, () async {
            diagnostics.rpc(method: method, state: 'start');
            diagnostics.rpc(
              method: method,
              state: 'failure',
              code: errorCode(deadlineError),
            );
            // Cleanup success must not hide the operation that failed.
            if (method != 'ChannelShutdown') {
              diagnostics.rpc(method: 'ChannelShutdown', state: 'start');
              diagnostics.rpc(method: 'ChannelShutdown', state: 'success');
            }
            throw deadlineError;
          }),
        );
        expect(events().last, containsPair('kind', 'result'));
        expect(events().last, containsPair('state', 'failure'));
        expect(events().last, containsPair('scenario', scenario));
        expect(events().last, containsPair('method', method));
        expect(events().last, containsPair('code', 'deadline_exceeded'));
        expect(events().where((e) => e['kind'] == 'result'), hasLength(1));
      });
    }
  }

  for (final scenario in ['wrong_host', 'missing_auth']) {
    test(
      '$scenario expected failure is distinct from terminal failure',
      () async {
        await diagnostics.run(() async {
          await diagnostics.scenario(scenario, () async {
            diagnostics.rpc(method: 'PutVertex', state: 'start');
            diagnostics.rpc(
              method: 'PutVertex',
              state: 'failure',
              code: errorCode(deadlineError),
            );
            throw deadlineError;
          });
          await diagnostics.scenario('marker', () async {});
        });
        final expected = events().where(
          (e) => e['state'] == 'expected_failure',
        );
        expect(expected.map((e) => e['kind']), ['rpc', 'scenario']);
        expect(events().where((e) => e['state'] == 'failure'), isEmpty);
        expect(events().last['state'], 'success');
      },
    );

    test('$scenario unexpected success remains blocking', () async {
      var reachedMarker = false;
      await diagnostics.run(() async {
        await diagnostics.scenario(scenario, () async {});
        reachedMarker = true;
        await diagnostics.scenario('marker', () async {});
      });
      expect(reachedMarker, isFalse);
      expect(events().last['state'], 'failure');
      expect(events().last['code'], 'unexpected_success');
    });
  }

  test(
    'terminal success waits for complete marker operation and cleanup',
    () async {
      final markerStarted = Completer<void>();
      final markerFinished = Completer<void>();
      final pending = diagnostics.run(
        () => diagnostics.scenario('marker', () async {
          diagnostics.rpc(method: 'PutVertex', state: 'success');
          markerStarted.complete();
          await markerFinished.future;
          diagnostics.rpc(method: 'ChannelShutdown', state: 'success');
        }),
      );
      await markerStarted.future;
      expect(events().where((e) => e['kind'] == 'result'), isEmpty);
      markerFinished.complete();
      await pending;
      expect(events().last['kind'], 'result');
      expect(events().last['state'], 'success');
      expect(events()[events().length - 3]['method'], 'ChannelShutdown');
    },
  );

  test(
    'cleanup failure retains the first failing RPC and its matching code',
    () async {
      await diagnostics.run(
        () => diagnostics.scenario('trusted_tls', () async {
          diagnostics.rpc(
            method: 'GetVertex',
            state: 'failure',
            code: 'deadline_exceeded',
          );
          diagnostics.rpc(method: 'ChannelShutdown', state: 'start');
          diagnostics.rpc(
            method: 'ChannelShutdown',
            state: 'failure',
            code: 'unavailable',
          );
          throw StateError('private cleanup error');
        }),
      );
      expect(
        events().where((e) => e['kind'] == 'rpc' && e['state'] == 'failure'),
        hasLength(2),
      );
      expect(events().last['method'], 'GetVertex');
      expect(events().last['code'], 'deadline_exceeded');
      expect(events().last['state'], 'failure');
    },
  );

  test('missing marker and assertion errors cannot produce success', () async {
    await diagnostics.run(() async {});
    expect(events().last['state'], 'failure');
    expect(events().last['code'], 'assertion');
    lines.clear();
    await diagnostics.run(
      () => diagnostics.scenario('trusted_tls', () async {
        throw StateError('Bearer secret-token result: personal-data');
      }),
    );
    expect(events().last['state'], 'failure');
    expect(events().last['code'], 'assertion');
    expect(lines.join(), isNot(contains('secret-token')));
    expect(lines.join(), isNot(contains('personal-data')));
  });

  test(
    'events retain only allowed codes and bounded basename stack positions',
    () async {
      final stack = StackTrace.fromString(
        [
          '#0 secretFunction (https://secret-endpoint/private/probe.dart:12:34)',
          '#1 secretFunction (/private/main.dart:56:78)',
          '#2 secretFunction (/private/secret.dart:90:12)',
          '#3 secretFunction (/private/probe.dart:12:34?secret-token)',
          '#4 secretFunction (/private/prefixprobe.dart:12:34)',
          for (var i = 0; i < 12; i++)
            '#5 secretFunction (/private/probe_diagnostics.dart:${i + 1}:2)',
        ].join('\n'),
      );
      await diagnostics.run(
        () => diagnostics.scenario('plaintext', () async {
          diagnostics.rpc(
            method: 'GetVertex',
            state: 'failure',
            code: 'secret-token',
            stackTrace: stack,
          );
          diagnostics.rpc(method: 'secret-method', state: 'failure');
          Error.throwWithStackTrace(deadlineError, stack);
        }),
      );
      expect(events().where((e) => e['kind'] == 'rpc'), hasLength(1));
      expect(events().firstWhere((e) => e['kind'] == 'rpc')['code'], 'unknown');
      for (final event in events()) {
        expect(event['schema'], 1);
        expect(event['transport'], transport);
        if (event['stack'] case final List<dynamic> frames) {
          expect(frames, hasLength(8));
          expect(frames.take(2), ['probe.dart:12:34', 'main.dart:56:78']);
        }
      }
      for (final line in lines) {
        expect(line, startsWith('LANTERN_PROBE_EVENT '));
        expect(line, isNot(contains('\n')));
        for (final secret in [
          'secret',
          'private',
          'https:',
          'Bearer',
          'personal-data',
        ]) {
          expect(line, isNot(contains(secret)));
        }
      }
    },
  );
}

class _DiagnosticStdout implements Stdout {
  _DiagnosticStdout(this.lines);

  final List<String> lines;

  @override
  void writeln([Object? object = '']) => lines.add('$object');

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
