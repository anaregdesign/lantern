import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:lantern_client/lantern_client.dart';

void main() {
  runApp(LanternExampleApp(configuration: DemoConfiguration.fromEnvironment()));
}

/// Non-secret runtime locations supplied with `--dart-define`.
final class DemoConfiguration {
  const DemoConfiguration({
    required this.endpoint,
    required this.tokenEndpoint,
    required this.allowInsecure,
  });

  final Uri endpoint;
  final Uri? tokenEndpoint;
  final bool allowInsecure;

  static DemoConfiguration? fromEnvironment() {
    const rawEndpoint = String.fromEnvironment('LANTERN_ENDPOINT');
    if (rawEndpoint.isEmpty) return null;
    const rawTokenEndpoint = String.fromEnvironment('LANTERN_TOKEN_ENDPOINT');
    return DemoConfiguration(
      endpoint: Uri.parse(rawEndpoint),
      tokenEndpoint: rawTokenEndpoint.isEmpty
          ? null
          : Uri.parse(rawTokenEndpoint),
      allowInsecure: const bool.fromEnvironment('LANTERN_ALLOW_INSECURE'),
    );
  }
}

/// Example root. The client is owned by the signed-in/app session below it.
class LanternExampleApp extends StatelessWidget {
  const LanternExampleApp({super.key, required this.configuration});

  final DemoConfiguration? configuration;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Lantern mobile example',
      theme: ThemeData(colorSchemeSeed: Colors.amber, useMaterial3: true),
      home: configuration == null
          ? const _ConfigurationHelp()
          : _ClientOwner(configuration: configuration!),
    );
  }
}

final class _RuntimeTokenProvider {
  _RuntimeTokenProvider(this.endpoint, {required bool allowInsecure}) {
    final uri = endpoint;
    if (uri != null && uri.scheme != 'https' && !allowInsecure) {
      throw ArgumentError.value(
        uri,
        'tokenEndpoint',
        'must use HTTPS unless LANTERN_ALLOW_INSECURE is explicitly enabled',
      );
    }
  }

  final Uri? endpoint;
  final HttpClient _http = HttpClient();

  /// Fetches a fresh user/device-scoped token from a BFF at call time.
  Future<String?> call() async {
    final uri = endpoint;
    if (uri == null) return null;
    final request = await _http.getUrl(uri);
    request.headers.set(HttpHeaders.acceptHeader, ContentType.json.mimeType);
    final response = await request.close();
    final body = await utf8.decodeStream(response);
    if (response.statusCode != HttpStatus.ok) {
      throw HttpException('token endpoint returned ${response.statusCode}');
    }
    final decoded = jsonDecode(body);
    if (decoded is! Map<String, Object?> ||
        decoded['access_token'] is! String ||
        (decoded['access_token']! as String).isEmpty) {
      throw const FormatException('token response needs access_token');
    }
    return decoded['access_token']! as String;
  }

  void close() => _http.close(force: true);
}

final class _ClientOwner extends StatefulWidget {
  const _ClientOwner({required this.configuration});

  final DemoConfiguration configuration;

  @override
  State<_ClientOwner> createState() => _ClientOwnerState();
}

final class _ClientOwnerState extends State<_ClientOwner> {
  late final _RuntimeTokenProvider _tokens;
  late final LanternClient _client;

  @override
  void initState() {
    super.initState();
    _tokens = _RuntimeTokenProvider(
      widget.configuration.tokenEndpoint,
      allowInsecure: widget.configuration.allowInsecure,
    );
    _client = LanternClient.connect(
      widget.configuration.endpoint,
      allowInsecure: widget.configuration.allowInsecure,
      tokenProvider: _tokens.call,
      retryPolicy: const RetryPolicy(),
      idempotentAdds: true,
    );
  }

  @override
  void dispose() {
    // A signed-in/app session owns the client. Transient inactive/background
    // lifecycle states do not close it.
    unawaited(_client.close());
    _tokens.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => _DiscoveryScreen(client: _client);
}

enum _UiPhase {
  loading,
  empty,
  ready,
  unauthenticated,
  unavailable,
  timeout,
  retryExhausted,
  error,
}

final class _DiscoveryScreen extends StatefulWidget {
  const _DiscoveryScreen({required this.client});

  final LanternClient client;

  @override
  State<_DiscoveryScreen> createState() => _DiscoveryScreenState();
}

final class _DiscoveryScreenState extends State<_DiscoveryScreen> {
  static const _prefix = 'flutter-demo:';
  static const _maxVisibleRows = 200;

  late final AppLifecycleListener _lifecycle;
  late final IncrementalSearch _search;
  late final StreamSubscription<SearchUpdate> _searchSubscription;
  late final ScrollController _scroll;
  LanternCancellationToken _screenCancellation = LanternCancellationToken();
  ScanCursor? _keysCursor;
  ScanCursor? _verticesCursor;
  var _loadingPage = false;
  var _phase = _UiPhase.loading;
  String _message = 'Loading';
  final List<String> _rows = [];

  @override
  void initState() {
    super.initState();
    _search = widget.client.incrementalSearch(
      options: const IncrementalSearchOptions(
        search: SearchOptions(prefix: _prefix),
      ),
    );
    _searchSubscription = _search.updates.listen(_onSearchUpdate);
    _scroll = ScrollController()..addListener(_onScroll);
    _lifecycle = AppLifecycleListener(
      onHide: _pauseScreenWork,
      onPause: _pauseScreenWork,
      // Intentionally no onInactive close/cancel: calls and system dialogs can
      // transiently mark an app inactive while it remains visible.
      onResume: _resumeAndRefetch,
    );
    unawaited(_refresh());
  }

  void _pauseScreenWork() {
    _screenCancellation.cancel('screen hidden');
    _search.search('');
  }

  void _resumeAndRefetch() {
    _screenCancellation = LanternCancellationToken();
    unawaited(_refresh());
  }

  Future<void> _refresh() async {
    _setPhase(_UiPhase.loading, 'Refreshing visible data');
    _keysCursor = null;
    _verticesCursor = null;
    _rows.clear();
    try {
      await _loadNextPage();
      if (_rows.isEmpty) {
        final coldStart = await widget.client.topVerticesByDegree(
          prefix: _prefix,
          direction: DegreeDirection.both,
          weighted: true,
          options: _callOptions,
        );
        _replaceRows(coldStart.map((entry) => 'cold-start ${entry.key}'));
      }
      _setPhase(
        _rows.isEmpty ? _UiPhase.empty : _UiPhase.ready,
        _rows.isEmpty ? 'No Lantern data yet' : 'Visible page data',
      );
    } catch (error) {
      _showFailure(error);
    }
  }

  LanternCallOptions get _callOptions => LanternCallOptions(
    timeout: const Duration(seconds: 5),
    cancellation: _screenCancellation,
  );

  Future<void> _loadNextPage() async {
    if (_loadingPage || (_keysCursor == null && _rows.isNotEmpty)) return;
    _loadingPage = true;
    try {
      // Keys-only is the efficient lazy-list backbone. Fetch the matching
      // value page separately only when this screen actually displays values.
      final keys = await widget.client.scanVertexKeys(
        prefix: _prefix,
        limit: 25,
        cursor: _keysCursor,
        options: _callOptions,
      );
      final vertices = await widget.client.scanVertices(
        prefix: _prefix,
        limit: 25,
        cursor: _verticesCursor,
        options: _callOptions,
      );
      _keysCursor = keys.nextCursor;
      _verticesCursor = vertices.nextCursor;
      _appendRows([
        ...keys.items.map((key) => 'key $key'),
        ...vertices.items.map(
          (vertex) =>
              'value ${vertex.key}: ${vertex.value.runtimeType} '
              'expires=${vertex.expiration?.toIso8601String() ?? 'never'}',
        ),
      ]);
    } finally {
      _loadingPage = false;
    }
  }

  void _onScroll() {
    if (_scroll.position.extentAfter < 300 && _keysCursor != null) {
      unawaited(_loadNextPage());
    }
  }

  Future<void> _seedCrud() async {
    _setPhase(_UiPhase.loading, 'Writing TTL-aware sample data');
    try {
      await widget.client.putVertices([
        VertexInput(
          key: '${_prefix}alice',
          value: VertexValue.string('quiet cafe graph'),
          expiresIn: const Duration(minutes: 30),
        ),
        VertexInput(
          key: '${_prefix}bob',
          value: VertexValue.uint64((BigInt.one << 63) + BigInt.one),
        ),
      ], options: _callOptions);
      await widget.client.getVertex('${_prefix}alice', options: _callOptions);
      await widget.client.putEdge(
        EdgeInput(
          tail: '${_prefix}alice',
          head: '${_prefix}bob',
          weight: 1,
          expiresIn: const Duration(minutes: 10),
        ),
        options: _callOptions,
      );
      await widget.client.addEdge(
        EdgeInput(
          tail: '${_prefix}alice',
          head: '${_prefix}bob',
          weight: 0.5,
          expiresIn: const Duration(minutes: 5),
        ),
        options: _callOptions,
      );
      await widget.client.getEdge(
        EdgeRef('${_prefix}alice', '${_prefix}bob'),
        options: _callOptions,
      );
      await widget.client.putVertex(
        VertexInput(key: '${_prefix}temporary', value: VertexValue.nil()),
        options: _callOptions,
      );
      await widget.client.deleteVertex(
        '${_prefix}temporary',
        options: _callOptions,
      );
      await _refresh();
    } catch (error) {
      _showFailure(error);
    }
  }

  void _onSearchUpdate(SearchUpdate update) {
    switch (update.phase) {
      case SearchUpdatePhase.idle:
        _setPhase(_UiPhase.empty, 'Search cleared');
      case SearchUpdatePhase.loading:
        _setPhase(_UiPhase.loading, 'Searching “${update.query}”');
      case SearchUpdatePhase.results:
        _replaceRows(update.hits.map((hit) => '${hit.key} ${hit.score}'));
        _setPhase(
          update.hits.isEmpty ? _UiPhase.empty : _UiPhase.ready,
          update.hits.isEmpty ? 'No search results' : 'Latest search results',
        );
      case SearchUpdatePhase.disabled:
        _setPhase(_UiPhase.empty, 'Search is disabled on this server');
      case SearchUpdatePhase.error:
        _showFailure(update.error ?? StateError('search failed'));
    }
  }

  Future<void> _traverse(TraversalOptions traversal) async {
    _setPhase(_UiPhase.loading, 'Loading graph navigation');
    try {
      final graph = await widget.client.illuminate(
        '${_prefix}alice',
        traversal: traversal,
        weighting: TraversalWeighting.raw,
        vertexPrefix: _prefix,
        options: _callOptions,
      );
      _replaceRows(
        graph.allEdges.map(
          (edge) =>
              '${edge.tail} → ${edge.head} weight=${edge.weight} '
              'expires=${edge.expiration?.toIso8601String() ?? 'never'}',
        ),
      );
      _setPhase(
        graph.allEdges.isEmpty ? _UiPhase.empty : _UiPhase.ready,
        '${traversal.runtimeType} graph',
      );
    } catch (error) {
      _showFailure(error);
    }
  }

  void _appendRows(Iterable<String> rows) {
    if (!mounted) return;
    setState(() {
      _rows.addAll(rows);
      if (_rows.length > _maxVisibleRows) {
        _rows.removeRange(0, _rows.length - _maxVisibleRows);
      }
    });
  }

  void _replaceRows(Iterable<String> rows) {
    if (!mounted) return;
    setState(() {
      _rows
        ..clear()
        ..addAll(rows.take(_maxVisibleRows));
    });
  }

  void _setPhase(_UiPhase phase, String message) {
    if (!mounted) return;
    setState(() {
      _phase = phase;
      _message = message;
    });
  }

  void _showFailure(Object error) {
    final (phase, message) = switch (error) {
      LanternUnauthenticatedException() => (
        _UiPhase.unauthenticated,
        'Sign in again: the token is missing or expired',
      ),
      LanternRetryExhaustedException() => (
        _UiPhase.retryExhausted,
        'The bounded retry budget was exhausted',
      ),
      LanternUnavailableException() => (
        _UiPhase.unavailable,
        'Lantern is currently unreachable',
      ),
      LanternDeadlineExceededException() => (
        _UiPhase.timeout,
        'The request timed out',
      ),
      _ => (_UiPhase.error, 'Unexpected failure: $error'),
    };
    _setPhase(phase, message);
  }

  @override
  void dispose() {
    _screenCancellation.cancel('screen disposed');
    unawaited(_searchSubscription.cancel());
    unawaited(_search.dispose());
    _lifecycle.dispose();
    _scroll.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Lantern mobile discovery')),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(12),
            child: TextField(
              key: const Key('search-field'),
              onChanged: _search.search,
              decoration: const InputDecoration(
                labelText: 'Latest-query-wins search',
                border: OutlineInputBorder(),
              ),
            ),
          ),
          Wrap(
            spacing: 8,
            children: [
              FilledButton(
                onPressed: _seedCrud,
                child: const Text('Seed CRUD'),
              ),
              OutlinedButton(
                onPressed: () =>
                    _traverse(const BfsOptions(step: 2, fanOut: 10)),
                child: const Text('BFS'),
              ),
              OutlinedButton(
                onPressed: () => _traverse(const PprOptions(topN: 20)),
                child: const Text('PPR'),
              ),
              OutlinedButton(
                onPressed: () =>
                    _traverse(const LocalCommunityOptions(maxSize: 20)),
                child: const Text('Community'),
              ),
            ],
          ),
          Semantics(
            liveRegion: true,
            child: Padding(
              padding: const EdgeInsets.all(8),
              child: Text('${_phase.name}: $_message', key: const Key('state')),
            ),
          ),
          Expanded(
            child: ListView.builder(
              controller: _scroll,
              itemCount: _rows.length,
              itemBuilder: (context, index) =>
                  ListTile(title: Text(_rows[index])),
            ),
          ),
        ],
      ),
    );
  }
}

final class _ConfigurationHelp extends StatelessWidget {
  const _ConfigurationHelp();

  @override
  Widget build(BuildContext context) {
    return const Scaffold(
      body: SafeArea(
        child: Padding(
          padding: EdgeInsets.all(24),
          child: Text(
            'Set LANTERN_ENDPOINT with --dart-define. '
            'Use LANTERN_TOKEN_ENDPOINT for a runtime short-lived-token BFF. '
            'Plaintext also requires LANTERN_ALLOW_INSECURE=true and is for '
            'debug/trusted-LAN use only.',
            key: Key('configuration-help'),
          ),
        ),
      ),
    );
  }
}
