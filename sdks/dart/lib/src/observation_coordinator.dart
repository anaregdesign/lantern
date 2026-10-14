part of 'client.dart';

/// Shared scheduling and bounded paused delivery. This retains no reusable reads.
final class _ObservationCoordinator {
  _ObservationCoordinator({
    required this.maxQueries,
    required this.maxSubscribers,
    required this.mailboxSize,
    required this.maxTraversalQueries,
  });
  final int maxQueries, maxSubscribers, mailboxSize, maxTraversalQueries;
  final Map<String, _ObservationQuery<Object?>> _queries = {};
  int _subscribers = 0;
  bool _closed = false;
  Stream<T> observe<T>(
    String key, {
    required T Function(bool) initial,
    required Future<T> Function(LanternCancellationToken) load,
    required T Function(T, bool) markLost,
    Duration? interval,
    bool Function(T)? pauseAfter,
    bool traversal = false,
  }) => _ObservationStream(() {
    if (_closed) throw const LanternDataException(DataFailure.closed);
    if (_subscribers >= maxSubscribers ||
        (!_queries.containsKey(key) && _queries.length >= maxQueries)) {
      throw const LanternDataException(DataFailure.capacity);
    }
    if (!_queries.containsKey(key) &&
        traversal &&
        _queries.values.where((q) => q.isTraversal).length >=
            maxTraversalQueries) {
      throw const LanternDataException(DataFailure.capacity);
    }
    final query =
        (_queries[key] ??= _ObservationQuery<T>(
              this,
              key,
              initial,
              load,
              markLost,
              interval,
              pauseAfter,
              traversal,
            ))
            as _ObservationQuery<T>;
    _subscribers++;
    return query.subscribe();
  });
  void invalidateKey(String key) => _queries[key]?.invalidate();
  void invalidate() {
    for (final query in _queries.values.toList()) {
      query.invalidate();
    }
  }

  Future<void> close() async {
    _closed = true;
    final queries = _queries.values.toList();
    for (final query in queries) {
      query.closedError();
    }
    await Future.wait(queries.map((q) => q.stop()));
    await Future.wait(
      queries.map((q) async {
        await q.running;
      }),
    );
    _queries.clear();
  }
}

final class _ObservationStream<T> extends Stream<T> {
  _ObservationStream(this._subscribe);
  final Stream<T> Function() _subscribe;
  @override
  StreamSubscription<T> listen(
    void Function(T)? onData, {
    Function? onError,
    void Function()? onDone,
    bool? cancelOnError,
  }) {
    try {
      return _subscribe().listen(
        onData,
        onError: onError,
        onDone: onDone,
        cancelOnError: cancelOnError,
      );
    } catch (error, stack) {
      return Stream<T>.error(error, stack).listen(
        onData,
        onError: onError,
        onDone: onDone,
        cancelOnError: cancelOnError,
      );
    }
  }
}

final class _ObservationQuery<T> {
  _ObservationQuery(
    this.owner,
    this.key,
    this.initial,
    this.load,
    this.markLost,
    this.interval,
    this.pauseAfter,
    this.isTraversal,
  );
  final _ObservationCoordinator owner;
  final String key;
  final T Function(bool) initial;
  final Future<T> Function(LanternCancellationToken) load;
  final T Function(T, bool) markLost;
  final Duration? interval;
  final bool Function(T)? pauseAfter;
  final bool isTraversal;
  T? pausedObservation;
  final Set<_ObservationSubscriber<T>> listeners = {};
  Timer? timer;
  LanternCancellationToken? token;
  Future<void>? running;
  bool dirty = false, stopped = false;
  int epoch = 0;
  Stream<T> subscribe() {
    late final _ObservationSubscriber<T> subscriber;
    subscriber = _ObservationSubscriber<T>(
      owner.mailboxSize,
      onCancel: () async {
        if (!listeners.remove(subscriber)) return;
        owner._subscribers--;
        if (listeners.isEmpty) await stop();
      },
      onOverflowResume: () => invalidate(),
    );
    listeners.add(subscriber);
    scheduleMicrotask(() {
      if (stopped || !listeners.contains(subscriber)) return;
      subscriber.offer(pausedObservation ?? initial(false));
      if (running == null && pausedObservation == null) {
        dirty = true;
        _start();
      }
    });
    return subscriber.controller.stream;
  }

  void invalidate({bool publishInitial = true}) {
    if (stopped) return;
    if (pausedObservation != null) {
      for (final listener in listeners) {
        listener.offer(markLost(pausedObservation as T, listener.lost));
      }
      return;
    }
    epoch++;
    if (publishInitial) {
      for (final listener in listeners) {
        listener.offer(initial(listener.lost));
      }
    }
    dirty = true;
    timer?.cancel();
    if (running == null) _start();
  }

  void _start() {
    if (stopped || !dirty || pausedObservation != null) return;
    dirty = false;
    token = LanternCancellationToken();
    final startedEpoch = epoch;
    running =
        Future<void>(() async {
          try {
            final result = await load(token!);
            if (pauseAfter?.call(result) ?? false) {
              pausedObservation = result;
              dirty = false;
            }
            if (!stopped && startedEpoch == epoch) {
              for (final listener in listeners.toList()) {
                listener.offer(markLost(result, listener.lost));
              }
            } else if (!stopped) {
              dirty = true;
            }
          } catch (error, stack) {
            if (!stopped) {
              for (final listener in listeners.toList()) {
                listener.fail(error, stack);
              }
              await stop(join: false);
            }
          }
        }).whenComplete(() {
          running = null;
          token = null;
          if (stopped) return;
          if (dirty) {
            scheduleMicrotask(_start);
          } else if (interval != null && pausedObservation == null) {
            timer = Timer(interval!, () => invalidate(publishInitial: false));
          }
        });
  }

  void closedError() {
    for (final listener in listeners.toList()) {
      listener.fail(
        const LanternDataException(DataFailure.closed),
        StackTrace.current,
      );
    }
  }

  Future<void> stop({bool join = true}) async {
    if (stopped) {
      if (join) await running;
      return;
    }
    stopped = true;
    timer?.cancel();
    token?.cancel();
    owner._queries.remove(key);
    final subscribers = listeners.toList();
    listeners.clear();
    for (final listener in subscribers) {
      owner._subscribers--;
      listener.finish();
    }
    if (join) await running;
  }
}

final class _ObservationSubscriber<T> {
  _ObservationSubscriber(
    this.capacity, {
    required Future<void> Function() onCancel,
    required this.onOverflowResume,
  }) {
    controller = StreamController<T>(
      sync: true,
      onPause: () => paused = true,
      onResume: () {
        paused = false;
        if (lost) {
          mailbox.clear();
          onOverflowResume();
        } else {
          _flush();
        }
      },
      onCancel: onCancel,
    );
  }
  final int capacity;
  final void Function() onOverflowResume;
  late final StreamController<T> controller;
  final Queue<T> mailbox = Queue();
  bool paused = false, lost = false, ended = false;
  bool emitting = false, flushScheduled = false;
  void offer(T value) {
    if (ended) return;
    if (paused || emitting || mailbox.isNotEmpty) {
      if (!lost) {
        if (mailbox.length == capacity) {
          mailbox.clear();
          lost = true;
        } else {
          mailbox.add(value);
        }
      }
      if (!paused) _scheduleFlush();
    } else {
      _emit(value);
    }
  }

  void _emit(T value) {
    emitting = true;
    lost = false;
    try {
      controller.add(value);
    } finally {
      emitting = false;
    }
  }

  void _scheduleFlush() {
    if (flushScheduled) return;
    flushScheduled = true;
    scheduleMicrotask(() {
      flushScheduled = false;
      _flush();
    });
  }

  void _flush() {
    if (paused || ended || emitting) return;
    if (lost) {
      mailbox.clear();
      onOverflowResume();
      return;
    }
    while (!paused && mailbox.isNotEmpty && !ended) {
      _emit(mailbox.removeFirst());
    }
  }

  bool failing = false;
  void fail(Object error, StackTrace stack) {
    if (ended || failing) return;
    failing = true;
    scheduleMicrotask(() => controller.addError(error, stack));
  }

  void finish() {
    ended = true;
    mailbox.clear();
    scheduleMicrotask(() => unawaited(controller.close()));
  }
}

/// Qualified storage-owner seam for the common bounded observation coordinator.
/// Retains only event delivery and in-flight work; adapters own evidence storage.
final class LanternObservationCoordinator {
  /// Reserves finite query/subscriber resources and eight-event mailboxes.
  LanternObservationCoordinator({
    int maxQueries = 64,
    int maxSubscribers = 64,
    int mailboxSize = 8,
  }) : _owner = _ObservationCoordinator(
         maxQueries: maxQueries,
         maxSubscribers: maxSubscribers,
         mailboxSize: mailboxSize,
         maxTraversalQueries: maxQueries,
       ) {
    if (maxQueries < 1 || maxSubscribers < 1 || mailboxSize < 1) {
      throw _invalidArgumentException('observation bounds must be positive');
    }
  }
  final _ObservationCoordinator _owner;

  /// Registers shared, bounded observation delivery.
  Stream<T> observe<T>(
    String key, {
    required T Function(bool) initial,
    required Future<T> Function(LanternCancellationToken) load,
    required T Function(T, bool) markLost,
    Duration? interval,
    bool Function(T)? pauseAfter,
  }) => _owner.observe(
    key,
    initial: initial,
    load: load,
    markLost: markLost,
    interval: interval,
    pauseAfter: pauseAfter,
  );

  /// Coalesces invalidations into one follow-up load per registered identity.
  void invalidate() => _owner.invalidate();

  /// Cancels and joins every owned observation.
  Future<void> close() => _owner.close();
}
