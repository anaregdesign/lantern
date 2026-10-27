import 'dart:async';

import 'probe_protocol.dart';

/// Fixed claim-proof contexts, independent of the crash/verify phase contract.
enum ClaimStage {
  seed,
  openClaimers,
  competingClaims,
  renewCas,
  killOwner,
  durableRenewalAfterOwnerDeath,
  expiredLeaseRecovery,
  freshProcessVerify,
  cleanup,
}

enum ClaimReason {
  timeout,
  unexpected,
  contract,
  spawn,
  malformedMessage,
  wrongEvent,
  unexpectedEof,
  messageOverflow,
  invalidPid,
  singleWinner,
  nonOwnerRenew,
  ownerRenew,
  beforeExpiryBlocked,
  recoveredOnce,
  staleRenew,
  killRefused,
  workerExit,
  collector,
  cleanup,
  arguments,
  workerMode,
  workerOwner,
  outboxCount,
  outboxRecord,
  operationCount,
  operationRecord,
  command,
}

enum ClaimWorkerStage {
  arguments,
  opening,
  seed,
  ready,
  claim,
  renew,
  beforeExpiry,
  recover,
  staleRenew,
  verify,
  stop,
  closing,
}

enum ClaimMode { seed, actor, verify }

enum ClaimSlot { seed, firstActor, secondActor, verifier }

enum ClaimEvent {
  started,
  ready,
  seeded,
  claimed,
  renewed,
  blocked,
  recovered,
  staleRenewed,
  verified,
  stopped,
  failed;

  String get wire => this == staleRenewed ? 'stale-renewed' : name;
}

enum ClaimPhase {
  launch,
  spawned,
  workerPhase,
  received,
  sent,
  kill,
  exited,
  cleanup,
  timeout,
}

/// Never retains an arbitrary error message or child payload.
class ClaimProblem implements Exception {
  const ClaimProblem(this.reason, {this.workerStage, this.workerReason});

  final ClaimReason reason;
  final ClaimWorkerStage? workerStage;
  final ClaimReason? workerReason;

  @override
  String toString() => 'claim_problem:${reason.name}';
}

ClaimReason claimReason(Object error) => switch (error) {
  ClaimProblem(:final reason) => reason,
  TimeoutException() => ClaimReason.timeout,
  FormatException() => ClaimReason.malformedMessage,
  StateError() => ClaimReason.contract,
  _ => ClaimReason.unexpected,
};

/// Primary and cleanup outcomes stay separate; cleanup never turns a failure
/// into success. The fixed peer matrix permits at most four nested cleanups.
final class ClaimProbeFailure extends ClaimProblem {
  ClaimProbeFailure(
    this.stage,
    ClaimReason reason, {
    ClaimWorkerStage? workerStage,
    ClaimReason? workerReason,
    this.cleanupAttempted = 0,
    this.cleanupSucceeded = 0,
    List<ClaimReason> cleanupFailures = const [],
  }) : cleanupFailures = List.unmodifiable(cleanupFailures),
       super(reason, workerStage: workerStage, workerReason: workerReason);

  final ClaimStage stage;
  final int cleanupAttempted;
  final int cleanupSucceeded;
  final List<ClaimReason> cleanupFailures;

  String get code => 'claim:${stage.name}:${reason.name}';

  Map<String, Object> snapshot() => {
    'stage': stage.name,
    'reason': reason.name,
    if (workerStage != null) 'worker_stage': workerStage!.name,
    if (workerReason != null) 'worker_reason': workerReason!.name,
    'cleanup_attempted': cleanupAttempted,
    'cleanup_succeeded': cleanupSucceeded,
    'cleanup_failures': [for (final reason in cleanupFailures) reason.name],
  };

  @override
  String toString() => code;
}

/// Only the typed supervisor failure may cross the crash gate's diagnostic boundary.
String? claimFailureCode(Object error) =>
    error is ClaimProbeFailure ? error.code : null;

/// Every peer cleanup runs, followed by directory cleanup, even on failure.
Future<T> withClaimCleanup<T>({
  required ClaimStage Function() stage,
  required Future<T> Function() body,
  required List<Future<void> Function()> Function() cleanups,
}) async {
  late T result;
  ClaimProbeFailure? primary;
  try {
    result = await body();
  } on Object catch (error) {
    primary = error is ClaimProbeFailure
        ? error
        : ClaimProbeFailure(
            stage(),
            claimReason(error),
            workerStage: error is ClaimProblem ? error.workerStage : null,
            workerReason: error is ClaimProblem ? error.workerReason : null,
          );
  }
  var attempted = 0;
  var succeeded = 0;
  final failures = <ClaimReason>[];
  for (final cleanup in cleanups()) {
    attempted++;
    try {
      await cleanup();
      succeeded++;
    } on Object catch (error) {
      failures.add(claimReason(error));
    }
  }
  if (primary != null || failures.isNotEmpty) {
    final failure =
        primary ?? ClaimProbeFailure(ClaimStage.cleanup, ClaimReason.cleanup);
    throw ClaimProbeFailure(
      failure.stage,
      failure.reason,
      workerStage: failure.workerStage,
      workerReason: failure.workerReason,
      cleanupAttempted: failure.cleanupAttempted + attempted,
      cleanupSucceeded: failure.cleanupSucceeded + succeeded,
      cleanupFailures: [...failure.cleanupFailures, ...failures],
    );
  }
  return result;
}

Map<String, Object> claimWorkerPhase(ClaimWorkerStage stage) => {
  'event': 'claim_phase',
  'stage': stage.name,
};

ClaimWorkerStage? parseClaimWorkerPhase(Map<String, Object?> message) {
  if (message.length != 2 || message['event'] != 'claim_phase') {
    return null;
  }
  for (final value in ClaimWorkerStage.values) {
    if (message['stage'] == value.name) {
      return value;
    }
  }
  return null;
}

Map<String, Object> claimWorkerFailure(ClaimWorkerStage stage, Object error) =>
    {'event': 'failed', 'stage': stage.name, 'reason': claimReason(error).name};

/// Started PID remains private protocol input for the existing kill target.
/// Public lifecycle snapshots expose only its equality with the direct child.
final class ClaimMessage {
  const ClaimMessage(
    this.event, {
    this.pid,
    this.count,
    this.accepted,
    this.failure,
  });
  final ClaimEvent event;
  final int? pid;
  final int? count;
  final bool? accepted;
  final ClaimProblem? failure;
}

ClaimMessage parseClaimMessage(Map<String, Object?> message) {
  final events = ClaimEvent.values.where(
    (event) => event.wire == message['event'],
  );
  if (events.length != 1) {
    throw const ClaimProblem(ClaimReason.malformedMessage);
  }
  final event = events.single;
  switch (event) {
    case ClaimEvent.started:
      if (message.length == 2 && message['pid'] is int) {
        return ClaimMessage(event, pid: message['pid']! as int);
      }
    case ClaimEvent.claimed || ClaimEvent.blocked || ClaimEvent.recovered:
      if (message.length == 2 && message['count'] is int) {
        return ClaimMessage(event, count: message['count']! as int);
      }
    case ClaimEvent.renewed || ClaimEvent.staleRenewed:
      if (message.length == 2 && message['accepted'] is bool) {
        return ClaimMessage(event, accepted: message['accepted']! as bool);
      }
    case ClaimEvent.failed:
      final stages = ClaimWorkerStage.values.where(
        (value) => value.name == message['stage'],
      );
      final reasons = ClaimReason.values.where(
        (value) => value.name == message['reason'],
      );
      if (message.length == 3 && stages.length == 1 && reasons.length == 1) {
        return ClaimMessage(
          event,
          failure: ClaimProblem(
            ClaimReason.workerExit,
            workerStage: stages.single,
            workerReason: reasons.single,
          ),
        );
      }
    case ClaimEvent.ready ||
        ClaimEvent.seeded ||
        ClaimEvent.verified ||
        ClaimEvent.stopped:
      if (message.length == 1) {
        return ClaimMessage(event);
      }
  }
  throw const ClaimProblem(ClaimReason.malformedMessage);
}

const maxClaimPendingMessages = 8;

/// Drain bounded JSON lines from spawn. Overflow/invalid output is latched,
/// never dropped to make a later acknowledgement appear successful.
final class ClaimMessages {
  ClaimMessages() {
    _lines = ProbeJsonLines(
      onMessage: _accept,
      onInvalid: () => _fail(const ClaimProblem(ClaimReason.malformedMessage)),
    );
  }
  late final ProbeJsonLines _lines;
  final _messages = <ClaimMessage>[];
  Completer<void>? _changed;
  ClaimProblem? _failure;
  bool _closed = false;
  ClaimEvent? lastExpected;
  ClaimEvent? lastObserved;
  ClaimProblem? workerFailure;
  ClaimProblem? workerCleanupFailure;

  void observeWorkerFailure(ClaimProblem failure) {
    if (workerFailure == null) {
      workerFailure = failure;
    } else if (failure.workerStage == ClaimWorkerStage.closing) {
      workerCleanupFailure ??= failure;
    }
  }

  void add(List<int> data) => _lines.add(data);
  void finish() {
    _lines.finish();
    _closed = true;
    _wake();
  }

  void _wake() {
    _changed?.complete();
    _changed = null;
  }

  void _fail(ClaimProblem error) {
    _failure ??= error;
    _wake();
  }

  void _accept(Map<String, Object?> raw) {
    if (_failure != null) {
      return;
    }
    try {
      final message = parseClaimMessage(raw);
      if (message.failure case final failure?) {
        observeWorkerFailure(failure);
      }
      if (_messages.length == maxClaimPendingMessages) {
        _fail(const ClaimProblem(ClaimReason.messageOverflow));
        return;
      }
      _messages.add(message);
      _wake();
    } on ClaimProblem catch (error) {
      _fail(error);
    }
  }

  Future<ClaimMessage> read(ClaimEvent event) async {
    lastExpected = event;
    while (_messages.isEmpty && _failure == null && !_closed) {
      _changed ??= Completer<void>();
      await _changed!.future;
    }
    if (_failure case final failure?) {
      throw failure;
    }
    if (_messages.isEmpty) {
      throw const ClaimProblem(ClaimReason.unexpectedEof);
    }
    final message = _messages.removeAt(0);
    lastObserved = message.event;
    if (message.failure case final failure?) {
      throw failure;
    }
    if (message.event != event) {
      throw const ClaimProblem(ClaimReason.wrongEvent);
    }
    return message;
  }

  void validate() {
    if (_failure case final failure?) {
      throw failure;
    }
    if (workerFailure case final failure?) {
      throw failure;
    }
    if (_messages.isNotEmpty) {
      throw const ClaimProblem(ClaimReason.wrongEvent);
    }
  }
}

/// Fixed-size content-free observations for one claim child; no authority.
final class ClaimLifecycle {
  ClaimLifecycle(this.mode, this.slot, {Duration Function()? elapsed}) {
    final clock = Stopwatch()..start();
    _elapsed = elapsed ?? () => clock.elapsed;
    record(ClaimPhase.launch);
  }
  final ClaimMode mode;
  final ClaimSlot slot;
  late final Duration Function() _elapsed;
  final _records = <Map<String, Object>>[];
  bool _truncated = false;
  String pidRelation = 'unobserved';
  final kills = <String, String>{};
  int? exit;
  ClaimWorkerStage? workerStage;
  int cleanupAttempted = 0;
  int cleanupSucceeded = 0;
  final cleanupFailures = <ClaimReason>[];

  void record(ClaimPhase phase) {
    if (_records.length == maxProbePhaseRecords) {
      _records.removeAt(0);
      _truncated = true;
    }
    _records.add({
      'phase': phase.name,
      'elapsed_us': _elapsed().inMicroseconds,
    });
  }

  Map<String, Object> snapshot(ClaimMessages? messages) => {
    'mode': mode.name,
    'slot': slot.name,
    'pid_relation': pidRelation,
    'truncated': _truncated,
    'direct_exit': ?exit,
    'kills': Map<String, String>.of(kills),
    if (cleanupAttempted != 0)
      'cleanup': {
        'attempted': cleanupAttempted,
        'succeeded': cleanupSucceeded,
        'failures': [for (final reason in cleanupFailures) reason.name],
      },
    if (messages?.lastExpected != null)
      'last_expected_event': messages!.lastExpected!.wire,
    if (messages?.lastObserved != null)
      'last_observed_event': messages!.lastObserved!.wire,
    if (workerStage != null) 'worker_stage': workerStage!.name,
    if (messages?.workerCleanupFailure?.workerReason != null)
      'worker_cleanup_failure_reason':
          messages!.workerCleanupFailure!.workerReason!.name,
    if (messages?.workerFailure?.workerStage != null)
      'worker_failure_stage': messages!.workerFailure!.workerStage!.name,
    if (messages?.workerFailure?.workerReason != null)
      'worker_failure_reason': messages!.workerFailure!.workerReason!.name,
    'records': [for (final record in _records) Map<String, Object>.of(record)],
  };
}
