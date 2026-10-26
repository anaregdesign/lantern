import 'dart:async';
import 'dart:convert';

import 'package:lantern_client/lantern_client.dart';

/// Fixed observations; their order never authorizes a crash boundary.
enum ProbePhase {
  launch,
  spawned,
  entered,
  referenceSeed,
  referenceReady,
  opening,
  opened,
  comparing,
  compared,
  closing,
  closed,
  completed,
  workerFailed,
  ready,
  sigkill,
  exited,
  timeout,
  cleanupKill,
  cleanupExit,
}

const _workerPhases = {
  ProbePhase.entered,
  ProbePhase.referenceSeed,
  ProbePhase.referenceReady,
  ProbePhase.opening,
  ProbePhase.opened,
  ProbePhase.comparing,
  ProbePhase.compared,
  ProbePhase.closing,
  ProbePhase.closed,
  ProbePhase.completed,
  ProbePhase.workerFailed,
};

/// The diagnostic line buffer never retains arbitrary unterminated output.
const maxProbeLineBytes = 512;

/// A child's diagnostic history retains only this many fixed observations.
const maxProbePhaseRecords = 32;

/// A worker observation carries no exception, argument, path or stored value.
Map<String, Object> probePhaseMarker(ProbePhase phase) {
  if (!_workerPhases.contains(phase)) {
    throw ArgumentError('invalid_worker_phase');
  }
  return {'event': 'probe_phase', 'phase': phase.name};
}

/// Separate a fixed marker from launcher progress lacking a trailing newline.
String probePhaseFrame(ProbePhase phase) =>
    '\n${jsonEncode(probePhaseMarker(phase))}\n';

/// Drains JSON lines with a bounded byte buffer, ignoring no protocol errors.
final class ProbeJsonLines {
  /// Callbacks receive only complete bounded Objects, or a fixed invalid flag.
  ProbeJsonLines({required this.onMessage, required this.onInvalid});

  /// Receives one decoded JSON Object.
  final void Function(Map<String, Object?>) onMessage;

  /// Receives no raw child text when a line is invalid or too large.
  final void Function() onInvalid;
  final _bytes = <int>[];
  var _discarding = false;

  /// Accept a pipe chunk without retaining more than [maxProbeLineBytes].
  void add(List<int> chunk) {
    for (final byte in chunk) {
      if (byte == 10) {
        _line();
      } else if (!_discarding) {
        if (_bytes.length == maxProbeLineBytes) {
          _bytes.clear();
          _discarding = true;
        } else {
          _bytes.add(byte);
        }
      }
    }
  }

  /// Complete the last line, including an oversized unterminated one.
  void finish() {
    if (_bytes.isNotEmpty || _discarding) _line();
  }

  void _line() {
    try {
      if (_discarding) {
        onInvalid();
        return;
      }
      final message = jsonDecode(utf8.decode(_bytes));
      if (message is! Map<String, Object?>) {
        onInvalid();
      } else {
        onMessage(message);
      }
    } on FormatException {
      onInvalid();
    } finally {
      _bytes.clear();
      _discarding = false;
    }
  }
}

/// Collect the existing single-Object verification response with a byte cap.
Future<Map<String, Object?>?> collectProbeMessage(
  Stream<List<int>> stream,
) async {
  final message = ProbeMessageBuffer();
  await for (final chunk in stream) {
    message.add(chunk);
  }
  return message.finish();
}

/// The same bounded single-Object parser for a supervised stdout collector.
final class ProbeMessageBuffer {
  final _bytes = <int>[];
  var _oversized = false;

  /// Drain every chunk, retaining at most [maxProbeLineBytes].
  void add(List<int> chunk) {
    if (!_oversized && _bytes.length + chunk.length <= maxProbeLineBytes) {
      _bytes.addAll(chunk);
    } else {
      _bytes.clear();
      _oversized = true;
    }
  }

  /// An incomplete or oversized Object never qualifies a verification.
  Map<String, Object?>? finish() {
    if (_oversized) return null;
    try {
      final message = jsonDecode(utf8.decode(_bytes));
      return message is Map<String, Object?> ? message : null;
    } on FormatException {
      return null;
    }
  }
}

/// Fixed names only; snapshots never contain an operating-system PID.
enum ProbeKill { worker, workerCleanup, directCleanup }

/// A pipe drains from spawn; all errors are consumed without retaining text.
final class ProbeCollector<T> {
  ProbeCollector(
    Stream<List<int>> stream, {
    required void Function(List<int>) onData,
    required T Function() onDone,
  }) {
    _subscription = stream.listen(
      (chunk) {
        if (!_accepting) return;
        try {
          onData(chunk);
        } on Object {
          _error = 'consumer';
        }
      },
      onError: (Object _) {
        if (_accepting) _error = 'stream';
      },
      onDone: () {
        if (_accepting) {
          try {
            _value = onDone();
          } on Object {
            _error = 'consumer';
          }
          _completion = 'complete';
        }
        if (!_done.isCompleted) _done.complete();
      },
    );
  }

  late final StreamSubscription<List<int>> _subscription;
  final _done = Completer<void>();
  var _completion = 'pending';
  var _error = 'none';
  var _cancellation = 'not_requested';
  var _accepting = true;
  T? _value;

  /// Never throws, even if the pipe fails before its owner begins waiting.
  Future<void> get done => _done.future;

  T? get value => _value;
  bool get complete => _completion == 'complete';
  bool get qualified => complete && _error == 'none';

  /// Stop accepting observations at the shared cleanup deadline.
  /// Cancellation itself gets no new wait allowance and may remain incomplete.
  Future<void> cancelIncomplete() async {
    if (complete) return;
    _accepting = false;
    _completion = 'incomplete';
    _cancellation = 'incomplete';
    try {
      await _subscription.cancel();
      _cancellation = 'complete';
    } on Object {
      _cancellation = 'error';
    }
  }

  /// No output bytes or exception strings cross the diagnostic boundary.
  Map<String, Object> snapshot() => {
    'completion': _completion,
    'error': _error,
    'cancellation': _cancellation,
  };
}

/// Bounded content-free observations for one supervisor-owned child.
final class ProbeLifecycle {
  /// Context comes from the fixed probe matrix, never child output.
  ProbeLifecycle({
    required this.mode,
    required this.scenario,
    required this.boundary,
    Duration Function()? elapsed,
  }) {
    if (!{'crash', 'verify'}.contains(mode) ||
        !{
          'schema',
          'enqueue',
          'claim',
          'confirmation',
          'cursor-chunk',
          'cursor-final',
          'checkpoint-reset',
          'scoped-partial',
          'scoped-final',
          'scoped-reset',
          'wipe',
          'receipt',
          'bound-receipt',
        }.contains(scenario) ||
        !{'before', 'after'}.contains(boundary)) {
      throw ArgumentError('invalid_probe_context');
    }
    final stopwatch = Stopwatch()..start();
    _elapsed = elapsed ?? () => stopwatch.elapsed;
    record(ProbePhase.launch);
  }

  /// Fixed child mode.
  final String mode;

  /// Fixed scenario name.
  final String scenario;

  /// Fixed transaction boundary.
  final String boundary;
  late final Duration Function() _elapsed;
  final _records = <Map<String, Object>>[];
  var _entered = false;
  var _truncated = false;
  var _workerPidRelation = 'unobserved';
  final _kills = {
    for (final kind in ProbeKill.values) kind.name: 'not_requested',
  };
  Map<String, Object> _directExit = {'state': 'unobserved'};
  final Map<String, ProbeCollector<Object?>> _collectors = {};

  /// Record only equality with the direct Process PID, never either PID.
  void observeWorkerPid({required bool sameAsDirect}) {
    _workerPidRelation = sameAsDirect
        ? 'same_as_direct'
        : 'different_from_direct';
  }

  /// Record the result of the existing signal call without issuing another.
  bool observeKill(ProbeKill kind, bool Function() kill) {
    try {
      final accepted = kill();
      _kills[kind.name] = accepted ? 'accepted' : 'refused';
      return accepted;
    } on Object {
      _kills[kind.name] = 'error';
      rethrow;
    }
  }

  /// A direct Process exit is not proof of any unobserved descendant's exit.
  void observeExit(int code) {
    _directExit = {
      'state': 'observed',
      'code': code,
      'category': code == 0 ? 'zero' : 'nonzero',
    };
  }

  /// Attach only the two fixed pipe names to this child's final snapshot.
  void collect({
    required ProbeCollector<Object?> stdout,
    required ProbeCollector<Object?> stderr,
  }) {
    _collectors.addAll({'stdout': stdout, 'stderr': stderr});
  }

  /// Record an observation without imposing an execution-phase ordering.
  void record(ProbePhase phase) {
    if (phase == ProbePhase.entered) _entered = true;
    if (_records.length == maxProbePhaseRecords) {
      _records.removeAt(0);
      _truncated = true;
    }
    _records.add({
      'phase': phase.name,
      'elapsed_us': _elapsed().inMicroseconds,
    });
  }

  /// Accept only exact, fixed worker phase markers; discard all other stderr.
  void observeWorker(Map<String, Object?> message) {
    if (message.length != 2 || message['event'] != 'probe_phase') return;
    for (final phase in _workerPhases) {
      if (message['phase'] == phase.name) {
        record(phase);
        return;
      }
    }
  }

  /// A fresh snapshot contains no PID, path, raw text, value or credential.
  Map<String, Object> snapshot() => {
    'mode': mode,
    'scenario': scenario,
    'boundary': boundary,
    'worker_entry_observed': _entered,
    'truncated': _truncated,
    'worker_pid_relation': _workerPidRelation,
    'kills': Map<String, String>.of(_kills),
    'direct_exit': Map<String, Object>.of(_directExit),
    'collectors': {
      for (final entry in _collectors.entries)
        entry.key: entry.value.snapshot(),
    },
    'records': [for (final record in _records) Map<String, Object>.of(record)],
  };
}

/// Observe the existing wait deadline without resetting it on child phases.
Future<T> waitForProbe<T>(
  Future<T> pending,
  Duration timeout,
  ProbeLifecycle lifecycle,
) async {
  try {
    return await pending.timeout(timeout);
  } on TimeoutException {
    lifecycle.record(ProbePhase.timeout);
    rethrow;
  }
}

/// Preserve the primary failure even when cleanup has a separate failure.
/// Optional validation reads already collected facts without another wait.
Future<T> withProbeCleanup<T>({
  required Future<T> Function() body,
  required Future<void> Function() cleanup,
  void Function()? validate,
}) async {
  late T result;
  Object? primary;
  StackTrace? primaryStack;
  try {
    result = await body();
  } on Object catch (error, stack) {
    primary = error;
    primaryStack = stack;
  }
  Object? cleanupError;
  StackTrace? cleanupStack;
  try {
    await cleanup();
  } on Object catch (error, stack) {
    cleanupError = error;
    cleanupStack = stack;
  }
  if (primary != null) Error.throwWithStackTrace(primary, primaryStack!);
  // An observed worker rejection remains primary over secondary cleanup I/O or
  // pipe failures. Missing/error output is left to cleanup's fail-closed verdict.
  validate?.call();
  if (cleanupError != null) {
    Error.throwWithStackTrace(cleanupError, cleanupStack!);
  }
  return result;
}

/// Preserve the existing verification parser and fixed worker rejection code.
void validateProbeVerification(
  int result,
  ProbeCollector<Map<String, Object?>?> stdout,
) {
  if (!stdout.qualified) return;
  final message = stdout.value;
  if (message == null) throw StateError('verification');
  final failure = parseProbeFailure(message);
  if (failure != null) throw StateError(failure);
  if (result != 0 || message['event'] != 'verified') {
    throw StateError('verification');
  }
}

/// Direct exit and both pipes share the existing single cleanup allowance.
Future<void> cleanupProbe({
  required bool exited,
  required bool Function() kill,
  required Future<int> exitCode,
  required Duration timeout,
  required ProbeLifecycle lifecycle,
}) async {
  final clock = Stopwatch()..start();
  Duration remaining() {
    final value = timeout - clock.elapsed;
    return value.isNegative ? Duration.zero : value;
  }

  var cleanupError = false;
  Future<void> stopDirect() async {
    if (exited) return;
    lifecycle.record(ProbePhase.cleanupKill);
    try {
      lifecycle.observeKill(ProbeKill.directCleanup, kill);
    } on Object {
      cleanupError = true;
    }
    try {
      lifecycle.observeExit(await exitCode);
      lifecycle.record(ProbePhase.cleanupExit);
    } on Object {
      lifecycle._directExit = {'state': 'error'};
      cleanupError = true;
    }
  }

  Object? deadlineError;
  StackTrace? deadlineStack;
  try {
    await waitForProbe(
      Future.wait([
        stopDirect(),
        for (final collector in lifecycle._collectors.values) collector.done,
      ]),
      remaining(),
      lifecycle,
    );
  } on Object catch (error, stack) {
    deadlineError = error;
    deadlineStack = stack;
    if (lifecycle._directExit['state'] == 'unobserved') {
      lifecycle._directExit = {'state': 'incomplete'};
    }
  } finally {
    // Each async cancellation consumes its own errors immediately. The shared
    // deadline is never reset, including when a hostile stream will not cancel.
    final cancellations = [
      for (final collector in lifecycle._collectors.values)
        collector.cancelIncomplete(),
    ];
    await Future.wait(cancellations).timeout(remaining(), onTimeout: () => []);
  }
  if (deadlineError != null) {
    Error.throwWithStackTrace(deadlineError, deadlineStack!);
  }
  if (cleanupError) throw StateError('cleanup');
  if (lifecycle._collectors.values.any((collector) => !collector.qualified)) {
    throw StateError('collector');
  }
}

/// Fixed, content-free stages shared by the crash worker and its supervisor.
enum ProbeStage {
  arguments,
  open,
  capability,
  seedVertices,
  seedEdges,
  enqueue,
  prepared,
  drain,
  unresolved,
  applied,
  reopen,
  reconcile,
  originals,
  ordinary,
}

Map<String, Object> probeFailureMarker(ProbeStage stage, Object error) => {
  'event': 'failed',
  'stage': stage.name,
  'code': switch (error) {
    LanternException(:final code) => 'transport_${code.name}',
    StateError() => 'contract',
    _ => 'unexpected',
  },
};

/// Reject arbitrary child data rather than copying it into public diagnostics.
String? parseProbeFailure(Map<String, Object?> message) {
  if (message.length != 3 || message['event'] != 'failed') return null;
  final stage = message['stage'];
  final code = message['code'];
  if (!ProbeStage.values.any((value) => value.name == stage) ||
      !{
        'contract',
        'unexpected',
        for (final value in LanternCode.values) 'transport_${value.name}',
      }.contains(code)) {
    return null;
  }
  return 'worker:$stage:$code';
}

bool isProbeFailureCode(String value) {
  final parts = value.split(':');
  return parts.length == 3 &&
      parts.first == 'worker' &&
      parseProbeFailure({
            'event': 'failed',
            'stage': parts[1],
            'code': parts[2],
          }) ==
          value;
}
