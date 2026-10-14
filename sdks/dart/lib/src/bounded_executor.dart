part of 'client.dart';

/// A bounded FIFO permit lane. It reserves queued work without starting tokens.
final class _BoundedPermits {
  _BoundedPermits(this.concurrency, this.queueCapacity);
  final int concurrency, queueCapacity;
  int active = 0;
  final Queue<_PermitWaiter> _queue = Queue();
  Future<void Function()> acquire(LanternCancellationToken token) {
    _throwIfCanceled(token);
    if (active < concurrency) {
      active++;
      return Future.value(_releaseOnce());
    }
    if (_queue.length >= queueCapacity) {
      throw const LanternDataException(DataFailure.capacity);
    }
    final waiter = _PermitWaiter();
    _queue.add(waiter);
    waiter.detach = token.listen((_) {
      if (!_queue.remove(waiter)) return;
      try {
        _throwIfCanceled(token);
      } catch (error, stack) {
        waiter.completion.completeError(error, stack);
      }
    });
    return waiter.completion.future;
  }

  void Function() _releaseOnce() {
    var released = false;
    return () {
      if (released) return;
      released = true;
      active--;
      if (_queue.isNotEmpty) {
        final next = _queue.removeFirst();
        next.detach?.call();
        active++;
        next.completion.complete(_releaseOnce());
      }
    };
  }
}

final class _PermitWaiter {
  final Completer<void Function()> completion = Completer();
  void Function()? detach;
}
