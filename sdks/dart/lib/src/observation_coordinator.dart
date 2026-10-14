part of 'client.dart';

/// Cache-neutral call guard for qualified adapters. The invoker checks it before
/// acquiring a token, after token acquisition and before publishing a reply.
/// A callback must be synchronous, content-free and must never perform I/O.
abstract final class LanternAdapterCancellation {
  /// Preserves the common typed caller cancellation at an adapter boundary.
  static void checkCanceled(LanternCancellationToken? cancellation) =>
      _throwIfCanceled(cancellation);

  /// Creates a token that composes attempt cancellation with a lifetime fence.
  static LanternCancellationToken guarded(void Function() checkCurrent) =>
      LanternCancellationToken._guarded(checkCurrent);
}

/// One-attempt bound read facade for a scheduler which already owns permits and
/// credential deadlines. Its close joins reads but leaves the transport to its
/// owning adapter. Ordinary Direct construction keeps all existing defaults.
abstract final class LanternAdapterReads {
  /// The adapter must reserve a finite permit before each network read and pass
  /// its checked work token. This facade supplies normalization and snapshots.
  static DirectLanternDataClient bound(
    LanternClient client, {
    required LanternSessionBinding binding,
    required void Function() checkAuthority,
    void Function(LanternException? failure)? onEndpoint,
  }) => DirectLanternDataClient(client, binding: binding)
    .._adapterEndpoint = onEndpoint
    .._adapterAuthority = checkAuthority
    .._adapterRead = true;
}

/// Resource class of a cache-neutral observation load.
enum LanternObservationKind {
  /// Ordinary finite network read.
  read,

  /// Expensive finite traversal.
  traversal,

  /// Bounded local capture; never requires credentials or a network permit.
  local,
}

/// One scheduler-owned attempt; query epochs remain the coordinator's concern.
abstract interface class LanternObservationWork {
  /// Token shared with the load, including post-token dispatch guards.
  LanternCancellationToken get cancellation;

  /// Rejects a stopped attempt before any result publication or side effect.
  void checkCurrent();
}

/// A bounded retained slot, independent of its running/dirty/polling state.
abstract interface class LanternObservationRegistration {
  /// Coalesces dirty work or requests an absolute poll/retry deadline.
  void request({DateTime? dueAt});

  /// Cancels and joins only this registration, releasing its capacity last.
  Future<void> cancelAndJoin();
}

/// Optional scheduling injection for adapters which already own shared permits,
/// deadlines and cancellation. The parent never acquires a backing-store owner.
abstract interface class LanternObservationScheduler {
  /// Scheduler time used to retain absolute polling deadlines.
  DateTime get now;

  /// Atomically reserves a finite slot or throws the typed capacity error.
  LanternObservationRegistration register(
    String key, {
    required LanternObservationKind kind,
    required Future<void> Function(LanternObservationWork) run,
  });
}

/// Shared scheduling and bounded paused delivery. This retains no reusable reads.
final class _ObservationCoordinator {
  _ObservationCoordinator({
    required this.maxQueries,
    required this.maxSubscribers,
    required this.mailboxSize,
    required this.maxTraversalQueries,
    this.scheduler,
  });
  final LanternObservationScheduler? scheduler;
  final int maxQueries, maxSubscribers, mailboxSize, maxTraversalQueries;
  final Map<String, _ObservationQuery<Object?>> _queries = {};
  int _subscribers = 0;
  bool _closed = false;
  Stream<T> observe<T>(
    String key, {
    required T Function(bool) initial,
    required Future<T> Function(LanternCancellationToken) load,
    required T Function(T, bool) markLost,
    Future<T> Function()? captureLocal,
    void Function(T)? onCurrent,
    void Function()? onStopped,
    Duration? interval,
    bool Function(T)? pauseAfter,
    bool traversal = false,
    bool local = false,
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
              captureLocal,
              onCurrent,
              onStopped,
              interval,
              pauseAfter,
              traversal,
              local,
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

  void resume() {
    for (final query in _queries.values.toList()) {
      query.pausedObservation = null;
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
    this.captureLocal,
    this.onCurrent,
    this.onStopped,
    this.interval,
    this.pauseAfter,
    this.isTraversal,
    bool local,
  ) {
    registration = owner.scheduler?.register(
      key,
      kind: local
          ? LanternObservationKind.local
          : isTraversal
          ? LanternObservationKind.traversal
          : LanternObservationKind.read,
      run: _runScheduled,
    );
  }
  late final LanternObservationRegistration? registration;
  final _ObservationCoordinator owner;
  final String key;
  final T Function(bool) initial;
  final Future<T> Function(LanternCancellationToken) load;
  final T Function(T, bool) markLost;
  final Future<T> Function()? captureLocal;
  final void Function(T)? onCurrent;
  final void Function()? onStopped;
  Future<void>? capturing;
  bool captureDirty = false;
  int publication = 0;
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
      _captureLocal();
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
    _captureLocal();
    if (publishInitial) {
      for (final listener in listeners) {
        listener.offer(initial(listener.lost));
      }
    }
    dirty = true;
    timer?.cancel();
    if (registration != null || running == null) _start();
  }

  void _start() {
    if (stopped || !dirty || pausedObservation != null) return;
    if (registration != null) {
      registration!.request();
      return;
    }
    dirty = false;
    token = LanternCancellationToken();
    final startedEpoch = epoch;
    running =
        Future<void>(() async {
          try {
            final result = await load(token!);
            if (!stopped && startedEpoch == epoch) {
              if (pauseAfter?.call(result) ?? false) {
                pausedObservation = result;
                dirty = false;
              }
              onCurrent?.call(result);
              publication++;
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

  Future<void> _runScheduled(LanternObservationWork work) {
    if (stopped || pausedObservation != null) return Future.value();
    dirty = false;
    final startedEpoch = epoch;
    token = work.cancellation;
    return running = (() async {
      try {
        work.checkCurrent();
        final result = await load(work.cancellation);
        work.checkCurrent();
        if (stopped) return;
        if (startedEpoch != epoch) {
          dirty = true;
          return;
        }
        if (pauseAfter?.call(result) ?? false) {
          pausedObservation = result;
          dirty = false;
        }
        onCurrent?.call(result);
        publication++;
        for (final listener in listeners.toList()) {
          listener.offer(markLost(result, listener.lost));
        }
      } catch (error, stack) {
        if (work.cancellation.isCanceled) {
          // Auth/suspend work cancellation preserves subscriber lifetimes.
          dirty = true;
          return;
        }
        if (!stopped) {
          for (final listener in listeners.toList()) {
            listener.fail(error, stack);
          }
          await stop(join: false);
        }
      } finally {
        running = null;
        token = null;
        if (!stopped &&
            !work.cancellation.isCanceled &&
            pausedObservation == null) {
          if (dirty) {
            registration!.request();
          } else if (interval != null) {
            registration!.request(dueAt: owner.scheduler!.now.add(interval!));
          }
        }
      }
    })();
  }

  void _captureLocal() {
    if (captureLocal == null || stopped) return;
    captureDirty = true;
    if (capturing != null) return;
    capturing =
        Future<void>(() async {
          // One bounded local capture and one dirty flag per reserved query. It
          // never waits for a network permit or chains a Future for each event.
          while (captureDirty && !stopped) {
            captureDirty = false;
            final capturedEpoch = epoch, capturedPublication = publication;
            try {
              final result = await captureLocal!();
              if (stopped ||
                  capturedEpoch != epoch ||
                  capturedPublication != publication) {
                continue;
              }
              onCurrent?.call(result);
              publication++;
              for (final listener in listeners.toList()) {
                listener.offer(markLost(result, listener.lost));
              }
            } catch (error, stack) {
              if (!stopped) {
                for (final listener in listeners.toList()) {
                  listener.fail(error, stack);
                }
                await stop(join: false);
              }
            }
          }
        }).whenComplete(() {
          capturing = null;
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
      if (join) {
        await running;
        await capturing;
      }
      return;
    }
    stopped = true;
    onStopped?.call();
    timer?.cancel();
    token?.cancel();
    final canceled = registration?.cancelAndJoin();
    owner._queries.remove(key);
    final subscribers = listeners.toList();
    listeners.clear();
    for (final listener in subscribers) {
      owner._subscribers--;
      listener.finish();
    }
    if (join) {
      await running;
      await canceled;
      await capturing;
    }
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
    LanternObservationScheduler? scheduler,
  }) : _owner = _ObservationCoordinator(
         maxQueries: maxQueries,
         maxSubscribers: maxSubscribers,
         mailboxSize: mailboxSize,
         maxTraversalQueries: maxQueries,
         scheduler: scheduler,
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
    Future<T> Function()? captureLocal,
    void Function(T)? onCurrent,
    void Function()? onStopped,
    Duration? interval,
    bool Function(T)? pauseAfter,
    bool traversal = false,
    bool local = false,
  }) => _owner.observe(
    key,
    initial: initial,
    load: load,
    markLost: markLost,
    captureLocal: captureLocal,
    onCurrent: onCurrent,
    onStopped: onStopped,
    interval: interval,
    pauseAfter: pauseAfter,
    traversal: traversal,
    local: local,
  );

  /// Invalidates one existing canonical registration without allocating a slot.
  void invalidateKey(String key) => _owner.invalidateKey(key);

  /// Coalesces invalidations into one follow-up load per registered identity.
  void invalidate() => _owner.invalidate();

  /// Clears query-local denial pauses after the adapter has accepted a verified
  /// authority/credential event and joined its previous work epoch.
  void resume() => _owner.resume();

  /// Cancels and joins every owned observation.
  Future<void> close() => _owner.close();
}
