import 'package:flutter/material.dart';
import 'package:lantern_client/lantern_client.dart';

void requireAppliedPut(String identity, PutOutcome outcome) {
  if (outcome != PutOutcome.appliedAndLive) {
    throw StateError('$identity was not live after Put: $outcome');
  }
}

void requireAppliedVertexPuts(List<VertexPutResult> results) {
  for (final result in results) {
    requireAppliedPut('vertex ${result.key}', result.outcome);
  }
}

/// A bounded, one-shot data demonstration owned by the current app account.
/// Its progress survives discovery refreshes and optional metadata failures.
final class DataDemoPanel extends StatefulWidget {
  const DataDemoPanel({
    super.key,
    required this.client,
    required this.prefix,
    required this.callOptions,
    required this.onDataChanged,
  });

  final LanternClient client;
  final String prefix;
  final LanternCallOptions Function() callOptions;
  final Future<void> Function() onDataChanged;

  @override
  State<DataDemoPanel> createState() => _DataDemoPanelState();
}

final class _DataDemoPanelState extends State<DataDemoPanel> {
  bool _seedStarted = false, _busy = false;
  String _step = '', _result = 'Seed CRUD runs once per screen session.';
  String _metadata = '';
  final List<String> _confirmed = [];

  Future<T> _runStep<T>(String name, Future<T> Function() run) async {
    // An old account must never start another operation after removal.
    if (!mounted) throw StateError('Demo screen disposed');
    setState(() => _step = name);
    final result = await run();
    if (mounted) setState(() => _confirmed.add(name));
    return result;
  }

  Future<void> _seed() async {
    if (_seedStarted || _busy) return;
    setState(() {
      _seedStarted = true;
      _busy = true;
      _result = 'Running Seed CRUD';
    });
    // Capture this run's cancellation token. A later lifecycle resume must not
    // give an interrupted mutation sequence a fresh token and continue it.
    final options = widget.callOptions();
    final prefix = widget.prefix;
    try {
      await _runStep('Put sample vertices', () async {
        final results = await widget.client.putVertices([
          VertexInput(
            key: '${prefix}alice',
            value: VertexValue.string('quiet cafe graph'),
            expiresIn: const Duration(minutes: 30),
          ),
          VertexInput(
            key: '${prefix}bob',
            value: VertexValue.uint64((BigInt.one << 63) + BigInt.one),
          ),
        ], options: options);
        // A mixed outcome must retain each confirmed write even though the
        // overall step cannot complete.
        if (mounted &&
            results.any((r) => r.outcome != PutOutcome.appliedAndLive)) {
          setState(
            () => _confirmed.addAll(
              results
                  .where((r) => r.outcome == PutOutcome.appliedAndLive)
                  .map((r) => 'Put vertex ${r.key}'),
            ),
          );
        }
        requireAppliedVertexPuts(results);
      });
      await _runStep(
        'Read sample vertex',
        () => widget.client.getVertex('${prefix}alice', options: options),
      );
      await _runStep('Put sample edge', () async {
        requireAppliedPut(
          'sample edge',
          await widget.client.putEdge(
            EdgeInput(
              tail: '${prefix}alice',
              head: '${prefix}bob',
              weight: 1,
              expiresIn: const Duration(minutes: 10),
            ),
            options: options,
          ),
        );
      });
      await _runStep(
        'Add sample edge',
        () => widget.client.addEdge(
          EdgeInput(
            tail: '${prefix}alice',
            head: '${prefix}bob',
            weight: 0.5,
            expiresIn: const Duration(minutes: 5),
          ),
          options: options,
        ),
      );
      await _runStep(
        'Read sample edge',
        () => widget.client.getEdge(
          EdgeRef('${prefix}alice', '${prefix}bob'),
          options: options,
        ),
      );
      final search = await _runStep(
        'Search sample vertices',
        () => widget.client.searchVertices(
          'quiet cafe',
          searchOptions: SearchOptions(
            prefix: prefix,
            matchMode: SearchMatchMode.all,
            limit: 10,
          ),
          options: options,
        ),
      );
      if (search is SearchDisabled) {
        if (mounted) {
          setState(() => _confirmed.add('Search disabled by server'));
        }
      } else {
        await _runStep(
          'Fuzzy search',
          () => widget.client.searchVertices(
            'serach',
            searchOptions: SearchOptions(prefix: prefix, fuzziness: 1),
            options: options,
          ),
        );
        await _runStep('Paged search', () async {
          await for (final _ in widget.client.searchVerticesStream(
            'quiet cafe',
            searchOptions: SearchOptions(
              prefix: prefix,
              limit: 2,
              projection: SearchProjection.fullVertex,
            ),
            options: options,
          )) {
            // Consume the full bounded result; terminal failures still fail
            // this step even if earlier pages were delivered.
          }
        });
        await _runStep('Cancelled search', () async {
          final cancellation = LanternCancellationToken()..cancel('example');
          try {
            await widget.client.searchVerticesPage(
              'quiet cafe',
              searchOptions: SearchOptions(prefix: prefix),
              options: LanternCallOptions(cancellation: cancellation),
            );
          } on LanternCanceledException {
            return;
          }
          throw StateError('Cancelled search unexpectedly completed');
        });
      }
      await _runStep('Put temporary vertex', () async {
        requireAppliedPut(
          'temporary vertex',
          await widget.client.putVertex(
            VertexInput(key: '${prefix}temporary', value: VertexValue.nil()),
            options: options,
          ),
        );
      });
      await _runStep(
        'Delete temporary vertex',
        () =>
            widget.client.deleteVertex('${prefix}temporary', options: options),
      );
      if (mounted) setState(() => _result = 'Seed CRUD completed');
    } catch (error) {
      if (mounted) {
        setState(
          () => _result =
              'Stopped at $_step: $error. Earlier confirmed steps remain. '
              'A failed write may still have applied; inspect its outcome before replaying.',
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
    // Discovery owns refresh errors. They cannot erase this run's progress or
    // turn a failed data/search operation into a successful demo result.
    if (mounted) await widget.onDataChanged();
  }

  Future<void> _inspectSearch() async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _step = '';
      _metadata = 'Loading Server search info';
    });
    final options = widget.callOptions();
    try {
      // GetServerStatus requires operations.read. This optional operation is
      // never a prerequisite for ordinary data/search work.
      final status = await widget.client.getServerStatus(options: options);
      final capability = status.search;
      if (!mounted) return;
      setState(
        () => _metadata =
            'search enabled=${capability.enabled} '
            'positions=${capability.positionsEnabled} '
            'analyzer=${capability.analyzerVersion} '
            'projection=${capability.projectionVersion} '
            'fingerprint=${capability.configFingerprint}',
      );
      if (capability.enabled && capability.positionsEnabled) {
        // Capability information may become stale; preserve a subsequent
        // query failure instead of treating it as optional metadata denial.
        try {
          final result = await widget.client.searchVertices(
            'quiet cafe',
            searchOptions: SearchOptions(prefix: widget.prefix, phrase: true),
            options: options,
          );
          if (mounted) {
            setState(
              () => _metadata += result is SearchDisabled
                  ? '\nPhrase search: search disabled by server'
                  : '\nPhrase search completed',
            );
          }
        } catch (error) {
          if (mounted) {
            setState(() => _metadata += '\nPhrase search failed: $error');
          }
        }
      }
    } on LanternPermissionDeniedException {
      if (mounted) {
        setState(
          () => _metadata =
              'Server search info unavailable: operations.read is required. '
              'Data/search results are independent.',
        );
      }
    } catch (error) {
      if (mounted) {
        setState(() => _metadata = 'Server search info failed: $error');
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Column(
    children: [
      Wrap(
        spacing: 8,
        children: [
          FilledButton(
            onPressed: _busy || _seedStarted ? null : _seed,
            child: const Text('Seed CRUD'),
          ),
          OutlinedButton(
            onPressed: _busy ? null : _inspectSearch,
            child: const Text('Server search info'),
          ),
        ],
      ),
      ConstrainedBox(
        constraints: const BoxConstraints(maxHeight: 140),
        child: SingleChildScrollView(
          child: Padding(
            padding: const EdgeInsets.all(8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(_result, key: const Key('data-demo-result')),
                if (_busy && _step.isNotEmpty) Text('Step: $_step'),
                if (_confirmed.isNotEmpty)
                  Text(
                    'Confirmed: ${_confirmed.join('; ')}',
                    key: const Key('data-demo-progress'),
                  ),
                if (_metadata.isNotEmpty)
                  Text(_metadata, key: const Key('data-demo-metadata')),
              ],
            ),
          ),
        ),
      ),
    ],
  );
}
