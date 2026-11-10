import 'dart:async';

import 'package:lantern_client/lantern_client.dart';

/// Observes the public scoped CDC cut and a later contribution Edge invalidation.
final class ContributionChangeWitness {
  ContributionChangeWitness(
    LanternClient client, {
    required String prefix,
    required EdgeRef edge,
  }) : _edge = edge {
    if (prefix.isEmpty ||
        !edge.tail.startsWith(prefix) ||
        !edge.head.startsWith(prefix)) {
      throw ArgumentError('Both contribution endpoints must share the scope');
    }
    // Either future may fail while the caller is still awaiting the other one.
    _ready.future.ignore();
    _invalidation.future.ignore();
    _subscription = client
        .watchChanges(
          prefix: prefix,
          projection: ChangeProjection.identity,
          bootstrap: true,
        )
        .listen(_observe, onError: _fail);
  }

  final EdgeRef _edge;
  final _ready = Completer<ChangeCursor>();
  final _invalidation = Completer<ChangeFrame>();
  late final StreamSubscription<ChangeFrame> _subscription;
  bool _closed = false;

  /// The actual opaque bootstrap checkpoint, never an origin/sequence map.
  Future<ChangeCursor> get ready => _ready.future;

  /// A matching frame; public CDC need not preserve mutation counts or grouping.
  Future<ChangeFrame> get invalidation => _invalidation.future;

  void _observe(ChangeFrame frame) {
    if (_closed) return;
    if (frame.bootstrap) {
      // The SDK validates the bootstrap order and nonempty opaque checkpoint.
      _ready.complete(frame.cursor!);
    } else if (!_invalidation.isCompleted &&
        frame.invalidations.any(
          (item) => item is EdgeInvalidation && item.edge == _edge,
        )) {
      _invalidation.complete(frame);
    }
  }

  void _fail(Object error, StackTrace stack) {
    if (_closed) return;
    if (!_ready.isCompleted) _ready.completeError(error, stack);
    if (!_invalidation.isCompleted) _invalidation.completeError(error, stack);
  }

  Future<void> close() async {
    _closed = true;
    await _subscription.cancel();
  }
}
