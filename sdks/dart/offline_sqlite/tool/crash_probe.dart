import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'claim_probe.dart';

const _scenarios = [
  'schema',
  'enqueue',
  'claim',
  'confirmation',
  'cursor-chunk',
  'cursor-final',
  'wipe',
];
const _timeout = Duration(seconds: 30);

/// Runs real SIGKILL/reopen boundaries and prints only aggregate case counts.
Future<void> main() async {
  if (Platform.isWindows) {
    stderr.writeln('crash_probe_requires_posix_sigkill');
    exitCode = 1;
    return;
  }
  final directory = await Directory.systemTemp.createTemp(
    'lantern-sqlite-crash-',
  );
  var verified = 0;
  var stage = 'start';
  try {
    for (final scenario in _scenarios) {
      for (final boundary in ['before', 'after']) {
        final path = '${directory.path}/$scenario-$boundary.db';
        stage = 'crash';
        await _crash(scenario, boundary, path);
        stage = 'verify';
        await _verify(scenario, boundary, path);
        verified++;
      }
    }
    stage = 'cross_process_claim';
    final claims = await runClaimProbe();
    stdout.writeln(
      jsonEncode({
        'scenarios': _scenarios.length,
        'sigkill': verified,
        'fresh_process_verified': verified,
        'cross_process_claim': claims,
      }),
    );
  } catch (_) {
    stderr.writeln('crash_probe_failed_after_$verified:$stage');
    exitCode = 1;
  } finally {
    await directory.delete(recursive: true);
  }
}

Future<Process> _start(
  String mode,
  String scenario,
  String boundary,
  String path,
) => Process.start(Platform.resolvedExecutable, [
  'run',
  File.fromUri(Platform.script.resolve('crash_worker.dart')).path,
  mode,
  scenario,
  boundary,
  path,
]);

Future<void> _crash(String scenario, String boundary, String path) async {
  final process = await _start('crash', scenario, boundary, path);
  final ready = Completer<int>();
  final stderrDrained = process.stderr.drain<void>();
  final subscription = process.stdout
      .transform(utf8.decoder)
      .transform(const LineSplitter())
      .listen(
        (line) {
          try {
            final message = jsonDecode(line) as Map<String, Object?>;
            if (message['event'] != 'ready' || ready.isCompleted) {
              throw StateError('marker');
            }
            ready.complete(message['pid']! as int);
          } catch (_) {
            if (!ready.isCompleted) ready.completeError(StateError('marker'));
          }
        },
        onDone: () {
          if (!ready.isCompleted) ready.completeError(StateError('early_exit'));
        },
      );
  int? workerPid;
  var killed = false;
  var exited = false;
  try {
    workerPid = await ready.future.timeout(_timeout);
    if (!Process.killPid(workerPid, ProcessSignal.sigkill)) {
      throw StateError('sigkill');
    }
    killed = true;
    final result = await process.exitCode.timeout(_timeout);
    exited = true;
    if (result == 0) throw StateError('normal_exit');
    await stderrDrained;
  } finally {
    if (!killed && workerPid != null && workerPid != process.pid) {
      Process.killPid(workerPid, ProcessSignal.sigkill);
    }
    if (!exited) {
      process.kill(ProcessSignal.sigkill);
      await process.exitCode.timeout(_timeout);
    }
    await subscription.cancel();
  }
}

Future<void> _verify(String scenario, String boundary, String path) async {
  final process = await _start('verify', scenario, boundary, path);
  var exited = false;
  final output = process.stdout.transform(utf8.decoder).join();
  final stderrDrained = process.stderr.drain<void>();
  try {
    final result = await process.exitCode.timeout(_timeout);
    exited = true;
    await stderrDrained;
    final message = jsonDecode(await output) as Map<String, Object?>;
    if (result != 0 || message['event'] != 'verified') {
      throw StateError('verification');
    }
  } finally {
    if (!exited) {
      process.kill(ProcessSignal.sigkill);
      await process.exitCode.timeout(_timeout);
    }
  }
}
