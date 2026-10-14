import 'dart:async';
import 'dart:collection';

import 'package:lantern_client/lantern_client.dart';

import 'automatic_owner.dart';

/// Work classes shared by push, incoming recovery and active observations.
enum OfflineSessionLane {
  /// Original receipt status recovery; reserves both read and send capacity.
  reconcile,

  /// One mutation attempt; any probe/status stage uses the reconcile lane.
  send,

  /// Ordinary entity observations and bounded resident recovery.
  read,

  /// Expensive query refresh with separate permits and a reserved ordinary read.
  traversal,
}

/// One cancellable wakeup. A runtime owns at most one outstanding wakeup.
abstract interface class OfflineSessionAlarm {
  /// Cancels delivery; a raced callback must still be fenced by its runtime.
  void cancel();
}

/// Clock-independent timer injection for finite deterministic qualification.
typedef OfflineSessionSchedule =
    OfflineSessionAlarm Function(Duration delay, void Function() callback);

/// A bounded job may publish only after checking its captured lifetime.
final class OfflineSessionWork {
  OfflineSessionWork._(this.cancellation, this._check);

  /// Attempt cancellation, independent of an application's completion waiter.
  final LanternCancellationToken cancellation;
  final void Function() _check;

  /// Checks owner/work epoch after awaits and immediately before publication.
  void checkCurrent() => _check();
}

/// Process-local shared permits. This does not acquire a backing-store owner.
/// Registering an owner is bounded; release gives waiting runtimes fair turns.
final class OfflineSessionBudget {
  /// Default global bounds preserve a read permit for ordinary observations.
  OfflineSessionBudget({
    this.maxSends = 4,
    this.maxReads = 8,
    this.maxTraversals = 2,
    this.maxRuntimes = 16,
    this.maxSendJobs = 128,
    this.maxReadJobs = 128,
    this.maxTraversalJobs = 64,
  }) {
    if (maxSends < 1 ||
        maxReads < 2 ||
        maxTraversals < 1 ||
        maxTraversals >= maxReads ||
        maxRuntimes < 1 ||
        maxSendJobs < 1 ||
        maxReadJobs < 1 ||
        maxTraversalJobs < 1) {
      throw const LanternDataException(DataFailure.capacity);
    }
  }

  /// Hard limits; traversal consumes both a read and an expensive-work permit.
  final int maxSends, maxReads, maxTraversals, maxRuntimes;

  /// Registered slots, including in-flight and dirty work, have global caps.
  final int maxSendJobs, maxReadJobs, maxTraversalJobs;
  final List<int> _jobCounts = [0, 0, 0];
  final Set<OfflineSessionRuntime> _owners = {};
  final Queue<OfflineSessionRuntime> _waiting = Queue();
  int _sends = 0, _reads = 0, _traversals = 0;
  bool _turnScheduled = false;

  void _reserve(OfflineSessionLane lane) {
    final category = _category(lane);
    final cap = [maxSendJobs, maxReadJobs, maxTraversalJobs][category];
    if (_jobCounts[category] == cap) {
      throw const LanternDataException(DataFailure.capacity);
    }
    _jobCounts[category]++;
  }

  void _retire(OfflineSessionLane lane) => _jobCounts[_category(lane)]--;

  bool _acquire(OfflineSessionLane lane) {
    if ((_usesSend(lane) && _sends == maxSends) ||
        (_usesRead(lane) && _reads == maxReads) ||
        (lane == OfflineSessionLane.traversal &&
            (_traversals == maxTraversals || _reads >= maxReads - 1))) {
      return false;
    }
    if (_usesSend(lane)) _sends++;
    if (_usesRead(lane)) _reads++;
    if (lane == OfflineSessionLane.traversal) _traversals++;
    return true;
  }

  void _release(OfflineSessionLane lane) {
    if (_usesSend(lane)) _sends--;
    if (_usesRead(lane)) _reads--;
    if (lane == OfflineSessionLane.traversal) _traversals--;
    _scheduleTurn();
  }

  void _wait(OfflineSessionRuntime runtime) {
    if (!_waiting.contains(runtime)) _waiting.add(runtime);
  }

  void _scheduleTurn() {
    if (_turnScheduled || _waiting.isEmpty) return;
    _turnScheduled = true;
    scheduleMicrotask(() {
      _turnScheduled = false;
      // Snapshot length: a blocked owner may register one later fair turn,
      // never spin until a still-owned permit becomes available.
      final count = _waiting.length;
      for (var i = 0; i < count; i++) {
        final owner = _waiting.removeFirst();
        if (_owners.contains(owner)) owner._pump();
      }
    });
  }
}

/// One owner-fenced, bounded foreground scheduler for a bound Store lifetime.
///
/// The exclusive adapter capability must already be acquired and validated.
/// This seam owns timers, fair permits, coalescing and cancellation; registered
/// jobs retain the existing durable preparation/receipt/TTL semantics. It does
/// not infer ownership from a filename, item lease or endpoint/network hint.
final class OfflineSessionRuntime {
  /// Acquires finite process-local resources without starting network work.
  OfflineSessionRuntime({
    required this.owner,
    required this.budget,
    required DateTime Function() clock,
    required this.fence,
    required this.onError,
    OfflineSessionSchedule? schedule,
    this.debounce = const Duration(milliseconds: 50),
    this.maximumWait = const Duration(milliseconds: 250),
    this.maxJobs = 64,
    this.maxSendJobs = 32,
    this.maxReadJobs = 32,
    this.maxTraversalJobs = 16,
    this.maxSends = 2,
    this.maxReads = 4,
    this.maxTraversals = 1,
  }) : _clock = clock,
       _schedule = schedule ?? _scheduleTimer,
       _ownerEpoch = owner.epoch {
    if (debounce < Duration.zero ||
        maximumWait < debounce ||
        maxJobs < 1 ||
        maxSendJobs < 1 ||
        maxReadJobs < 1 ||
        maxTraversalJobs < 1 ||
        maxSends < 1 ||
        maxReads < 2 ||
        maxTraversals < 1 ||
        maxTraversals >= maxReads) {
      throw const LanternDataException(DataFailure.capacity);
    }
    _checkOwner();
    if (budget._owners.length == budget.maxRuntimes) {
      throw const LanternDataException(DataFailure.capacity);
    }
    budget._owners.add(this);
  }

  /// Exclusive lifetime; release remains the factory's responsibility after join.
  final OfflineAutomaticOwner owner;

  /// Shared global read/send/traversal bounds across independently owned scopes.
  final OfflineSessionBudget budget;

  /// Account, credential and authority checks supplied by the bound factory.
  final void Function() fence;

  /// Reports a job failure once, without inventing a retry or clearing a pause.
  final void Function(Object error, StackTrace stack) onError;

  /// Fixed debounce for repeated dirty events.
  final Duration debounce, maximumWait;

  /// Retained job capacities for all work and each category in this scope.
  final int maxJobs, maxSendJobs, maxReadJobs, maxTraversalJobs;

  /// Concurrent scoped permits, also charged to the shared global budget.
  final int maxSends, maxReads, maxTraversals;
  final DateTime Function() _clock;
  final OfflineSessionSchedule _schedule;
  final int _ownerEpoch;
  final Map<String, _Job> _jobs = {};
  final Set<Future<void>> _flights = {};
  final Map<OfflineSessionLane, Queue<_Job>> _queues = {
    for (final lane in OfflineSessionLane.values) lane: Queue(),
  };
  OfflineSessionAlarm? _alarm;
  DateTime? _highWater;
  int _workEpoch = 0, _alarmEpoch = 0, _laneCursor = 0;
  int _sends = 0, _reads = 0, _traversals = 0;
  bool _closed = false, _suspended = false, _pumping = false;
  Future<void>? _closing, _suspending;

  /// Retained job slots include in-flight work and its one dirty follow-up.
  int get jobCount => _jobs.length;

  /// Owned callbacks must finish before the runtime can release its owner.
  int get inFlightCount => _flights.length;

  /// Includes waiting for a fair permit, coalescing and in-flight dirty work.
  bool get deferred => _jobs.values.any((j) => j.pending);

  /// At most one regardless of outbox size or active observation count.
  bool get hasWakeup => _alarm != null;

  DateTime _now() {
    final sample = _clock().toUtc();
    if (_highWater == null || sample.isAfter(_highWater!)) _highWater = sample;
    return _highWater!;
  }

  void _checkOwner() {
    if (_closed || owner.epoch != _ownerEpoch) {
      throw const LanternDataException(DataFailure.closed);
    }
    owner.checkCurrent();
    fence();
  }

  /// Registers or dirties one canonical job slot. Hot events cannot postpone
  /// eligible work beyond [maximumWait]. Explicit persisted deadlines retain
  /// their absolute time and do not restart mutation age or attempt policy.
  void enqueue(
    String key,
    OfflineSessionLane lane,
    Future<void> Function(OfflineSessionWork work) run, {
    DateTime? dueAt,
  }) {
    _checkOwner();
    if (key.isEmpty || key.length > 1024) {
      throw const LanternDataException(DataFailure.capacity);
    }
    final existing = _jobs[key];
    if (existing?.removing ?? false) {
      throw const LanternDataException(DataFailure.closed);
    }
    if (existing != null && existing.lane != lane) {
      throw const LanternDataException(DataFailure.capacity);
    }
    if (existing == null) {
      final category = _jobs.values
          .where((j) => _category(j.lane) == _category(lane))
          .length;
      final bound = _usesSend(lane)
          ? maxSendJobs
          : lane == OfflineSessionLane.traversal
          ? maxTraversalJobs
          : maxReadJobs;
      if (_jobs.length == maxJobs || category == bound) {
        throw const LanternDataException(DataFailure.capacity);
      }
    }
    final now = _now();
    final job = existing ?? _Job(key, lane, run);
    if (existing == null) {
      budget._reserve(lane);
      _jobs[key] = job;
      _queues[lane]!.add(job);
    }
    job.run = run;
    if (dueAt != null) {
      // An explicit deadline is a scheduling constraint, never a network proof.
      final requested = dueAt.toUtc();
      job.due = !job.pending || job.due == null || requested.isBefore(job.due!)
          ? requested
          : job.due;
    } else {
      job.firstDirty ??= now;
      final latest = now.add(debounce);
      final maximum = job.firstDirty!.add(maximumWait);
      final coalesced = latest.isBefore(maximum) ? latest : maximum;
      // Do not push an already explicit/eligible deadline backwards.
      job.due =
          job.explicitDeadline &&
              job.due != null &&
              job.due!.isBefore(coalesced)
          ? job.due
          : coalesced;
    }
    job.explicitDeadline |= dueAt != null;
    job.pending = true;
    _arm();
  }

  /// Unregisters queued work and cancels/joins its current callback. Admitted
  /// durable mutations are separate; removing a job never withdraws an intent.
  Future<void> remove(String key) async {
    final job = _jobs[key];
    if (job == null) return;
    job.removing = true;
    _queues[job.lane]!.remove(job);
    job.pending = false;
    job.token?.cancel();
    _arm();
    await job.flight;
    if (identical(_jobs[key], job)) {
      _jobs.remove(key);
      budget._retire(job.lane);
    }
  }

  bool _scopePermit(OfflineSessionLane lane) =>
      (!_usesSend(lane) || _sends < maxSends) &&
      (!_usesRead(lane) || _reads < maxReads) &&
      (lane != OfflineSessionLane.traversal ||
          (_traversals < maxTraversals && _reads < maxReads - 1));

  void _pump() {
    if (_closed || _suspended || _pumping) return;
    _pumping = true;
    try {
      _checkOwner();
      final now = _now();
      while (true) {
        _Job? selected;
        // Weighted RR 2/2/2/1 with FIFO rotation within each finite lane.
        for (var turn = 0; turn < _weights.length; turn++) {
          final lane = _weights[_laneCursor];
          _laneCursor = (_laneCursor + 1) % _weights.length;
          if (!_scopePermit(lane)) continue;
          final queue = _queues[lane]!;
          for (var i = 0, count = queue.length; i < count; i++) {
            final job = queue.removeFirst();
            queue.add(job);
            if (!job.pending || job.flight != null || job.due!.isAfter(now)) {
              continue;
            }
            if (!budget._acquire(lane)) {
              budget._wait(this);
              break;
            }
            selected = job;
            break;
          }
          if (selected != null) break;
        }
        if (selected == null) break;
        _start(selected);
      }
    } catch (error, stack) {
      // A lost owner cannot spin an expired timer; the factory will join/close.
      _suspended = true;
      _workEpoch++;
      for (final job in _jobs.values) {
        job.token?.cancel();
      }
      onError(error, stack);
    } finally {
      _pumping = false;
      _arm(wakeDue: false);
    }
  }

  void _start(_Job job) {
    if (_usesSend(job.lane)) _sends++;
    if (_usesRead(job.lane)) _reads++;
    if (job.lane == OfflineSessionLane.traversal) _traversals++;
    final epoch = _workEpoch;
    final token = LanternCancellationToken();
    job.token = token;
    job.pending = false;
    job.firstDirty = null;
    job.explicitDeadline = false;
    final run = job.run;
    final work = OfflineSessionWork._(token, () {
      _checkOwner();
      if (_suspended ||
          epoch != _workEpoch ||
          token.isCanceled ||
          !identical(_jobs[job.key], job)) {
        throw const LanternDataException(DataFailure.closed);
      }
    });
    late final Future<void> flight;
    flight =
        Future<void>(() async {
          try {
            work.checkCurrent();
            await run(work);
          } catch (error, stack) {
            if (!token.isCanceled && !_closed && !_suspended) {
              onError(error, stack);
            }
          }
        }).whenComplete(() {
          job.flight = null;
          job.token = null;
          _flights.remove(flight);
          if (_usesSend(job.lane)) _sends--;
          if (_usesRead(job.lane)) _reads--;
          if (job.lane == OfflineSessionLane.traversal) _traversals--;
          budget._release(job.lane);
          if (identical(_jobs[job.key], job) && !job.pending && !job.removing) {
            _jobs.remove(job.key);
            _queues[job.lane]!.remove(job);
            budget._retire(job.lane);
          }
          // Permit release first schedules other waiting owners. This owner joins
          // the same bounded queue so a hot scope cannot immediately reacquire all.
          if (!_closed && !_suspended) {
            budget._wait(this);
            budget._scheduleTurn();
          }
          _arm(wakeDue: false);
        });
    job.flight = flight;
    _flights.add(flight);
  }

  void _arm({bool wakeDue = true}) {
    _alarm?.cancel();
    _alarm = null;
    final epoch = ++_alarmEpoch;
    if (_closed || _suspended) return;
    final now = _now();
    DateTime? first;
    for (final job in _jobs.values) {
      if (!job.pending || job.flight != null) continue;
      // Due work waiting for permits is woken by permit release, not a zero
      // timer loop. A new enqueue still gets one immediate fair turn.
      if (!job.due!.isAfter(now)) {
        budget._wait(this);
        if (wakeDue) first = now;
        continue;
      }
      if (first == null || job.due!.isBefore(first)) first = job.due;
    }
    if (first == null) return;
    _alarm = _schedule(first.difference(now), () {
      if (_closed || _suspended || epoch != _alarmEpoch) return;
      _alarm = null;
      _pump();
    });
  }

  /// Cancels and joins work before credential/authority rotation. Retained
  /// queued jobs resume only through the factory's verified lifetime fence.
  Future<void> suspend() {
    if (_closed) return _closing ?? Future.value();
    if (_suspending != null) return _suspending!;
    _suspended = true;
    _workEpoch++;
    _alarm?.cancel();
    _alarm = null;
    _alarmEpoch++;
    for (final job in _jobs.values) {
      if (job.flight != null) {
        job.pending = true;
        job.due = _now();
      }
      job.token?.cancel();
    }
    return _suspending = Future.wait(_flights.toList()).then((_) {});
  }

  /// Foreground restart only after the canceled work has joined. This does not
  /// clear durable authentication, policy, storage or restore holds.
  void resume() {
    _checkOwner();
    if (_flights.isNotEmpty) {
      throw const LanternDataException(DataFailure.closed);
    }
    _suspending = null;
    _suspended = false;
    _pump();
  }

  /// Idempotently cancels and joins every owned callback, then releases only
  /// process-local permits/registrations. Factory releases exclusive owner last.
  Future<void> close() {
    if (_closing != null) return _closing!;
    _closed = true;
    _workEpoch++;
    _alarmEpoch++;
    _alarm?.cancel();
    _alarm = null;
    for (final job in _jobs.values) {
      job.token?.cancel();
    }
    budget._waiting.remove(this);
    budget._owners.remove(this);
    _closing = Future.wait(_flights.toList()).then((_) {
      for (final job in _jobs.values) {
        budget._retire(job.lane);
      }
      _jobs.clear();
      for (final queue in _queues.values) {
        queue.clear();
      }
    });
    return _closing!;
  }
}

const _weights = [
  OfflineSessionLane.reconcile,
  OfflineSessionLane.reconcile,
  OfflineSessionLane.send,
  OfflineSessionLane.send,
  OfflineSessionLane.read,
  OfflineSessionLane.read,
  OfflineSessionLane.traversal,
];
bool _usesSend(OfflineSessionLane lane) =>
    lane == OfflineSessionLane.send || lane == OfflineSessionLane.reconcile;
bool _usesRead(OfflineSessionLane lane) => lane != OfflineSessionLane.send;
int _category(OfflineSessionLane lane) => _usesSend(lane)
    ? 0
    : lane == OfflineSessionLane.traversal
    ? 2
    : 1;

final class _Job {
  _Job(this.key, this.lane, this.run);
  final String key;
  final OfflineSessionLane lane;
  Future<void> Function(OfflineSessionWork) run;
  DateTime? firstDirty, due;
  bool pending = false, explicitDeadline = false, removing = false;
  LanternCancellationToken? token;
  Future<void>? flight;
}

final class _TimerAlarm implements OfflineSessionAlarm {
  _TimerAlarm(this.timer);
  final Timer timer;
  @override
  void cancel() => timer.cancel();
}

OfflineSessionAlarm _scheduleTimer(Duration delay, void Function() callback) =>
    _TimerAlarm(Timer(delay, callback));
