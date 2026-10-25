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
  final bytes = <int>[];
  var oversized = false;
  await for (final chunk in stream) {
    if (!oversized && bytes.length + chunk.length <= maxProbeLineBytes) {
      bytes.addAll(chunk);
    } else {
      bytes.clear();
      oversized = true;
    }
  }
  if (oversized) return null;
  try {
    final message = jsonDecode(utf8.decode(bytes));
    return message is Map<String, Object?> ? message : null;
  } on FormatException {
    return null;
  }
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

/// Preserve the existing bounded wrapper cleanup and record its actual outcome.
Future<void> cleanupProbe({
  required bool exited,
  required bool Function() kill,
  required Future<int> exitCode,
  required Duration timeout,
  required ProbeLifecycle lifecycle,
}) async {
  if (exited) return;
  lifecycle.record(ProbePhase.cleanupKill);
  kill();
  await waitForProbe(exitCode, timeout, lifecycle);
  lifecycle.record(ProbePhase.cleanupExit);
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
