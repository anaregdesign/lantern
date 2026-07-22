import 'dart:async';

import 'package:flutter/material.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';

/// Maintained Firebase-like offline UX over the public offline package.
class OfflineDemoScreen extends StatefulWidget {
  const OfflineDemoScreen({
    super.key,
    required this.repository,
    required this.partitionId,
    this.vertexKey = 'flutter-offline:profile',
    this.edgeTail = 'flutter-offline:profile',
    this.edgeHead = 'flutter-offline:counter',
  });

  final OfflineLanternRepository repository;
  final String partitionId;
  final String vertexKey;
  final String edgeTail;
  final String edgeHead;

  @override
  State<OfflineDemoScreen> createState() => _OfflineDemoScreenState();
}

final class _OfflineDemoScreenState extends State<OfflineDemoScreen> {
  late final TextEditingController _value;
  late final AppLifecycleListener _lifecycle;
  StreamSubscription<OfflineSnapshot<Vertex>>? _vertexSubscription;
  StreamSubscription<OfflineSnapshot<Edge>>? _edgeSubscription;
  final List<StreamSubscription<OfflineWriteStatus>> _writeSubscriptions = [];
  LanternCancellationToken _cancellation = LanternCancellationToken();
  OfflineSnapshot<Vertex>? _vertex;
  OfflineSnapshot<Edge>? _edge;
  OfflineWriteStatus? _writeStatus;
  List<DeadLetterSummary> _deadLetters = const [];
  var _busy = false;
  var _paused = false;
  var _message = 'Opening local cache';

  @override
  void initState() {
    super.initState();
    _value = TextEditingController(text: 'Ada');
    _startWatches();
    _lifecycle = AppLifecycleListener(
      onHide: _pause,
      onPause: _pause,
      onResume: _resume,
      onDetach: _pause,
    );
    unawaited(_refreshRecovery());
  }

  void _startWatches() {
    unawaited(_vertexSubscription?.cancel());
    unawaited(_edgeSubscription?.cancel());
    final cancellation = _cancellation;
    void onWatchError(Object error, StackTrace stackTrace) {
      if (!identical(cancellation, _cancellation) || _isCancellation(error)) {
        return;
      }
      _showFailure(error, stackTrace);
    }

    _vertexSubscription = widget.repository
        .watchVertex(
          widget.partitionId,
          widget.vertexKey,
          initialPolicy: OfflineReadPolicy.cacheFirst,
          allowStale: true,
          cancellation: cancellation,
        )
        .listen(
          (snapshot) => _mountedSet(() => _vertex = snapshot),
          onError: onWatchError,
        );
    _edgeSubscription = widget.repository
        .watchEdge(
          widget.partitionId,
          EdgeRef(widget.edgeTail, widget.edgeHead),
          initialPolicy: OfflineReadPolicy.cacheFirst,
          allowStale: true,
          cancellation: cancellation,
        )
        .listen(
          (snapshot) => _mountedSet(() => _edge = snapshot),
          onError: onWatchError,
        );
  }

  void _pause() {
    _paused = true;
    _cancellation.cancel('offline screen hidden');
    final vertex = _vertexSubscription;
    final edge = _edgeSubscription;
    _vertexSubscription = null;
    _edgeSubscription = null;
    unawaited(vertex?.cancel());
    unawaited(edge?.cancel());
  }

  void _resume() {
    _paused = false;
    _cancellation = LanternCancellationToken();
    _startWatches();
    unawaited(_probeAndReplay());
  }

  Future<void> _saveVertex() async {
    await _run('Saving locally', () async {
      final handle = await widget.repository.putVertex(
        partitionId: widget.partitionId,
        input: VertexInput(
          key: widget.vertexKey,
          value: VertexValue.string(_value.text),
          expiresIn: const Duration(minutes: 30),
        ),
      );
      _watchWrite(handle);
      _message = 'Saved locally; remote confirmation is separate';
    });
  }

  Future<void> _addEdge() async {
    await _run('Saving Add locally', () async {
      final handle = await widget.repository.addEdge(
        partitionId: widget.partitionId,
        input: EdgeInput(
          tail: widget.edgeTail,
          head: widget.edgeHead,
          weight: 0.25,
          expiresIn: const Duration(minutes: 10),
        ),
      );
      _watchWrite(handle);
      _message = 'Add saved locally with a persisted contribution ID';
    });
  }

  void _watchWrite(OfflineWriteHandle handle) {
    late final StreamSubscription<OfflineWriteStatus> subscription;
    subscription = handle.statuses.listen(
      (status) {
        _mountedSet(() {
          _writeStatus = status;
          _message = 'Write ${status.state.name}';
        });
      },
      onError: _showFailure,
      onDone: () => _writeSubscriptions.remove(subscription),
    );
    _writeSubscriptions.add(subscription);
  }

  Future<void> _replay() => _run('Replaying eligible work', () async {
    final confirmed = await widget.repository.drain(
      widget.partitionId,
      cancellation: _cancellation,
    );
    _message = 'Replay confirmed $confirmed item(s)';
    await _refreshRecovery();
  });

  Future<void> _probeAndReplay() =>
      _run('Probing Lantern before replay', () async {
        final confirmed = await widget.repository.probeAndDrain(
          widget.partitionId,
          cancellation: _cancellation,
        );
        _message = 'Probe succeeded; confirmed $confirmed item(s)';
        await _refreshRecovery();
      });

  Future<void> _retryDeadLetter(String recordId) =>
      _run('Returning dead letter to the queue', () async {
        await widget.repository.retryDeadLetter(widget.partitionId, recordId);
        await _refreshRecovery();
        _message = 'Dead letter queued without rebasing TTL or Add ID';
      });

  Future<void> _deleteDeadLetter(String recordId) =>
      _run('Deleting dead letter', () async {
        await widget.repository.deleteDeadLetter(widget.partitionId, recordId);
        await _refreshRecovery();
        _message = 'Dead letter deleted';
      });

  Future<void> _inspectDeadLetter(DeadLetterSummary summary) async {
    final authorized =
        await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            title: const Text('Inspect sensitive local intent?'),
            content: const Text(
              'The default summary hides graph keys and values. Continue only '
              'after the application has authorized this signed-in user.',
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: const Text('Cancel'),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('Inspect'),
              ),
            ],
          ),
        ) ??
        false;
    if (!authorized || !mounted) return;
    await _run('Inspecting authorized intent', () async {
      final intent = await widget.repository.inspectDeadLetter(
        widget.partitionId,
        summary.recordId,
        authorize: (_) => true,
      );
      _message = intent == null
          ? 'Dead letter changed before inspection'
          : 'Authorized intent category: ${intent.category.name}';
    });
  }

  Future<void> _refreshRecovery() async {
    final deadLetters = await widget.repository.listDeadLetters(
      widget.partitionId,
    );
    _mountedSet(() => _deadLetters = deadLetters);
  }

  Future<void> _run(String progress, Future<void> Function() action) async {
    if (_busy) return;
    _mountedSet(() {
      _busy = true;
      _message = progress;
    });
    try {
      await action();
    } catch (error) {
      _showFailure(error);
    } finally {
      _mountedSet(() => _busy = false);
    }
  }

  void _showFailure(Object error, [StackTrace? _]) {
    if (_paused && _isCancellation(error)) return;
    final message = switch (error) {
      OfflineRemoteFailure(:final kind) => 'Remote ${kind.name}; still local',
      OfflineCanceledException() => 'Foreground replay canceled',
      OfflineCapacityException() => 'Local offline capacity is full',
      _ => 'Offline operation failed: ${error.runtimeType}',
    };
    _mountedSet(() => _message = message);
  }

  bool _isCancellation(Object error) => switch (error) {
    OfflineCanceledException() => true,
    OfflineRemoteFailure(kind: OfflineRemoteErrorKind.canceled) => true,
    _ => false,
  };

  void _mountedSet(void Function() update) {
    if (mounted) setState(update);
  }

  @override
  void dispose() {
    _cancellation.cancel('offline screen disposed');
    unawaited(_vertexSubscription?.cancel());
    unawaited(_edgeSubscription?.cancel());
    for (final subscription in _writeSubscriptions) {
      unawaited(subscription.cancel());
    }
    _lifecycle.dispose();
    _value.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final vertex = _vertex;
    final edge = _edge;
    return Scaffold(
      appBar: AppBar(title: const Text('Lantern offline Repository')),
      body: ListView(
        key: const Key('offline-demo-list'),
        padding: const EdgeInsets.all(16),
        children: [
          Text(
            'This opt-in screen acknowledges local durability before remote '
            'confirmation. It never promises delivery while suspended or killed.',
            style: Theme.of(context).textTheme.bodyMedium,
          ),
          const SizedBox(height: 12),
          _SnapshotCard(
            title: 'Cached Vertex',
            snapshot: vertex,
            value: switch (vertex?.value?.value) {
              null => null,
              StringValue(:final value) => value,
              final value => value.runtimeType.toString(),
            },
            keyPrefix: 'offline-vertex',
          ),
          const SizedBox(height: 12),
          TextField(
            key: const Key('offline-value-field'),
            controller: _value,
            decoration: const InputDecoration(
              labelText: 'Profile value',
              border: OutlineInputBorder(),
            ),
          ),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              FilledButton(
                key: const Key('offline-save-local'),
                onPressed: _busy ? null : _saveVertex,
                child: const Text('Save locally'),
              ),
              OutlinedButton(
                key: const Key('offline-replay'),
                onPressed: _busy ? null : _replay,
                child: const Text('Replay now'),
              ),
              OutlinedButton(
                key: const Key('offline-probe-replay'),
                onPressed: _busy ? null : _probeAndReplay,
                child: const Text('Probe & replay'),
              ),
            ],
          ),
          const SizedBox(height: 16),
          _SnapshotCard(
            title: 'Additive Edge',
            snapshot: edge,
            value: edge?.value?.weight.toString(),
            keyPrefix: 'offline-edge',
          ),
          Align(
            alignment: Alignment.centerLeft,
            child: OutlinedButton(
              key: const Key('offline-add-local'),
              onPressed: _busy ? null : _addEdge,
              child: const Text('Add 0.25 locally'),
            ),
          ),
          const SizedBox(height: 12),
          Semantics(
            liveRegion: true,
            child: Text(_message, key: const Key('offline-message')),
          ),
          Text(
            'Last write: ${_writeStatus?.state.name ?? 'none'}',
            key: const Key('offline-write-status'),
          ),
          const Divider(height: 32),
          Text(
            'Dead letters (${_deadLetters.length})',
            key: const Key('offline-dead-letter-count'),
            style: Theme.of(context).textTheme.titleMedium,
          ),
          for (final summary in _deadLetters)
            ListTile(
              title: Text('${summary.category.name}: ${summary.state.name}'),
              subtitle: Text(
                'attempts=${summary.attemptCount} '
                'code=${summary.diagnosticCode ?? 'none'}',
              ),
              trailing: Wrap(
                children: [
                  IconButton(
                    tooltip: 'Inspect',
                    onPressed: () => _inspectDeadLetter(summary),
                    icon: const Icon(Icons.visibility_outlined),
                  ),
                  IconButton(
                    tooltip: 'Retry',
                    onPressed: () => _retryDeadLetter(summary.recordId),
                    icon: const Icon(Icons.replay),
                  ),
                  IconButton(
                    tooltip: 'Delete',
                    onPressed: () => _deleteDeadLetter(summary.recordId),
                    icon: const Icon(Icons.delete_outline),
                  ),
                ],
              ),
            ),
          const SizedBox(height: 12),
          const Text(
            'Conditional Put and Delete are intentionally unavailable offline: '
            'their response-loss outcomes require server reconciliation.',
          ),
        ],
      ),
    );
  }
}

final class _SnapshotCard<T> extends StatelessWidget {
  const _SnapshotCard({
    required this.title,
    required this.snapshot,
    required this.value,
    required this.keyPrefix,
  });

  final String title;
  final OfflineSnapshot<T>? snapshot;
  final String? value;
  final String keyPrefix;

  @override
  Widget build(BuildContext context) {
    final current = snapshot;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(title, style: Theme.of(context).textTheme.titleMedium),
            Text(
              current == null
                  ? 'loading'
                  : '${current.state.name} / '
                        '${current.source?.name ?? (current.hasPendingWrites ? 'local-overlay' : 'no-source')}',
              key: Key('$keyPrefix-state'),
            ),
            if (value != null) Text(value!, key: Key('$keyPrefix-value')),
            Wrap(
              spacing: 8,
              children: [
                Chip(
                  key: Key('$keyPrefix-pending'),
                  label: Text(
                    current?.hasPendingWrites ?? false
                        ? 'pending'
                        : 'confirmed',
                  ),
                ),
                if (current?.isEstimate ?? false)
                  Chip(
                    key: Key('$keyPrefix-estimate'),
                    label: const Text('estimate'),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
