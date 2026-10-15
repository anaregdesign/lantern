import 'dart:async';

import 'package:flutter/material.dart';
import 'package:lantern_client/lantern_client.dart';

import 'app_data_session.dart';

/// The same consumer in every ready profile. All refresh/retry scheduling belongs
/// to the bound client. A UI listener owns only its cancellation and display.
final class DataObservationsScreen extends StatefulWidget {
  const DataObservationsScreen({
    super.key,
    required this.session,
    required this.prefix,
  });
  final AppDataSession session;
  final String prefix;
  @override
  State<DataObservationsScreen> createState() => _DataObservationsScreenState();
}

final class _DataObservationsScreenState extends State<DataObservationsScreen> {
  LanternDataClient get data => widget.session.data!;
  String get keyName => '${widget.prefix}alice';
  EdgeRef get edge => EdgeRef(keyName, '${widget.prefix}bob');
  final _text = TextEditingController(text: 'Hello Lantern');
  final List<StreamSubscription<Object?>> _watches = [];
  late final AppLifecycleListener _lifecycle;
  late final StreamSubscription<void> _retired;
  LanternCancellationToken _wait = LanternCancellationToken();
  Future<void> _joined = Future.value();
  String _vertex = 'Unknown',
      _edge = 'Unknown',
      _traversal = 'Unknown',
      _sync = 'Unknown';
  String _result = 'Ordinary writes wait for Server handling.';
  String _admission = 'No explicit submission.';
  StreamSubscription<MutationStatus<Object?>>? _status;
  bool _busy = false, _visible = true;

  @override
  void initState() {
    super.initState();
    _subscribe();
    _retired = widget.session.retired.listen((_) {
      _visible = false;
      _pause();
      if (mounted) {
        setState(() {
          _vertex = _edge = _traversal = 'Account closed';
        });
        unawaited(Navigator.of(context).maybePop());
      }
    });
    _lifecycle = AppLifecycleListener(
      onHide: _pause,
      onPause: _pause,
      onResume: _resume,
    );
  }

  void _watch<T>(Stream<T> stream, void Function(T) display) {
    _watches.add(
      stream.listen(
        (value) {
          if (mounted && _visible && !widget.session.isClosed) {
            setState(() => display(value));
          }
        },
        onError: (Object _) {
          if (mounted && _visible) {
            setState(() => _sync = 'Observation unavailable');
          }
        },
      ),
    );
  }

  void _subscribe() {
    if (_watches.isNotEmpty || widget.session.isClosed) return;
    _watch(data.watchVertex(keyName), (v) {
      final value = v.view?.value;
      _vertex =
          '${v.confirmed.state.name}: ${value is StringValue ? value.value : ''} '
          'pending=${v.pending.count} ${v.projection.name}';
    });
    _watch(
      data.watchEdge(edge),
      (v) => _edge =
          '${v.confirmed.state.name}: ${v.view?.weight ?? ''} pending=${v.pending.count}',
    );
    _watch(
      data.watchIlluminate(
        keyName,
        traversal: const BfsOptions(step: 2, fanOut: 10),
        vertexPrefix: widget.prefix,
      ),
      (v) => _traversal =
          '${v.confirmed.state.name}: ${v.confirmed.value?.allEdges.length ?? 0} edges; ${v.observationMode.name}',
    );
    _watch(
      data.syncStatus,
      (v) => _sync =
          '${v.reachability.name}; ${v.pause.name}; pending=${v.activeMutations}; attention=${v.terminalAttentionItems}',
    );
  }

  void _pause() {
    _visible = false;
    _wait.cancel();
    final watches = _watches.toList();
    _watches.clear();
    _joined = _joined.then((_) async {
      await Future.wait(watches.map((s) => s.cancel()));
      await widget.session.suspend();
    });
    _observeLifecycle();
  }

  void _resume() {
    _joined = _joined.then((_) async {
      if (!mounted || widget.session.isClosed) return;
      await widget.session.resume();
      if (!mounted || widget.session.isClosed) return;
      _visible = true;
      _wait = LanternCancellationToken();
      _subscribe();
    });
    _observeLifecycle();
  }

  void _observeLifecycle() {
    // Retain the failed join so later lifecycle transitions cannot step around
    // it. Consume the notification Future and keep the error visible to the UI.
    unawaited(
      _joined.then<void>(
        (_) {},
        onError: (Object _, StackTrace _) {
          if (mounted) {
            setState(
              () =>
                  _sync = 'Session unavailable; retirement requires attention',
            );
          }
        },
      ),
    );
  }

  VertexInput _input() => VertexInput(
    key: keyName,
    value: VertexValue.string(_text.text),
    expiresIn: const Duration(minutes: 30),
  );
  MutationOptions get _options => MutationOptions(
    admissionCancellation: _wait,
    wait: WaitOptions(
      cancellation: _wait,
      timeout: const Duration(seconds: 15),
    ),
  );

  Future<void> _ordinary(bool delete) async {
    setState(() {
      _busy = true;
      _result = 'Waiting for Server handling';
    });
    try {
      final reply = delete
          ? await data.deleteVertex(keyName, options: _options)
          : await data.putVertex(_input(), options: _options);
      if (mounted && _visible) {
        setState(
          () => _result = switch (reply) {
            MutationKnownEffect(:final effect) => 'Server effect: $effect',
            MutationAcceptedUndisclosed() =>
              'Server accepted; effect undisclosed',
          },
        );
      }
    } catch (error) {
      if (mounted && _visible) {
        setState(
          () => _result = error is LanternDataException
              ? 'Write: ${error.reason.name}. Admitted work is not canceled by ending a wait.'
              : 'Write unavailable; handling not confirmed.',
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _submit() async {
    setState(() => _busy = true);
    try {
      final ticket = await data.mutations.submit(
        MutationRequest.putVertices([_input()]),
        options: MutationOptions(admissionCancellation: _wait),
      );
      await _status?.cancel();
      if (!mounted || !_visible) return;
      setState(
        () => _admission =
            'Locally admitted: ${ticket.durability.name}; Server handling pending',
      );
      _status = data.mutations
          .watchStatus(ticket.reference.operationId)
          .listen(
            (status) {
              if (mounted && _visible && !widget.session.isClosed) {
                setState(
                  () => _admission =
                      '${ticket.durability.name}: ${status.items.map((i) => i.state.name).join(', ')}',
                );
              }
            },
            onError: (Object _) {
              if (mounted) setState(() => _admission = 'Status unavailable');
            },
          );
    } catch (_) {
      if (mounted && _visible) setState(() => _admission = 'Admission failed');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  void dispose() {
    _visible = false;
    _wait.cancel();
    for (final subscription in _watches) {
      unawaited(subscription.cancel());
    }
    unawaited(_status?.cancel());
    unawaited(_retired.cancel());
    _lifecycle.dispose();
    _text.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('Live data')),
    body: ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Text('Vertex: $_vertex', key: const Key('live-vertex')),
        Text('Edge: $_edge', key: const Key('live-edge')),
        Text('Traversal: $_traversal', key: const Key('live-traversal')),
        Text(_sync, key: const Key('live-sync')),
        TextField(
          controller: _text,
          decoration: const InputDecoration(labelText: 'Value'),
        ),
        Wrap(
          spacing: 8,
          children: [
            FilledButton(
              onPressed: _busy ? null : () => _ordinary(false),
              child: const Text('Put'),
            ),
            OutlinedButton(
              onPressed: _busy ? null : () => _ordinary(true),
              child: const Text('Delete'),
            ),
            OutlinedButton(
              onPressed: _busy ? null : _submit,
              child: const Text('Submit locally'),
            ),
          ],
        ),
        Text(_result, key: const Key('live-result')),
        Text(_admission, key: const Key('live-admission')),
      ],
    ),
  );
}
