part of 'client.dart';

/// How a multi-term query selects matching vertices.
enum SearchMatchMode {
  /// Match at least one analyzed term.
  any,

  /// Match every analyzed term.
  all,

  /// Match at least [SearchOptions.minShouldMatch] analyzed terms.
  minShouldMatch,
}

/// Optional full-text relevance controls.
///
/// Leaving every field `null` omits the wire options message so the server's
/// configured defaults remain authoritative.
final class SearchOptions {
  /// Creates one immutable search configuration.
  const SearchOptions({
    this.limit = 0,
    this.prefix = '',
    this.matchMode,
    this.minShouldMatch,
    this.phrase,
    this.fuzziness,
    this.prefixTerms,
  });

  /// Maximum hits; zero selects the server default.
  final int limit;

  /// Optional vertex-key namespace scope.
  final String prefix;

  /// Optional query-term membership mode.
  final SearchMatchMode? matchMode;

  /// Required positive term count for [SearchMatchMode.minShouldMatch].
  final int? minShouldMatch;

  /// Whether word terms must occur adjacently and in order.
  final bool? phrase;

  /// Maximum edit distance: 0, 1, or 2.
  final int? fuzziness;

  /// Whether query words also match dictionary extensions.
  final bool? prefixTerms;
}

/// One immutable BM25-ranked hit; equal scores use raw [key] ascending.
final class SearchHit {
  /// Creates a ranked hit.
  const SearchHit({required this.key, required this.score});

  /// Matching vertex key.
  final String key;

  /// BM25 relevance score; higher is more relevant.
  final double score;
}

/// Result of one search, including the server-configured disabled state.
sealed class SearchResult {
  const SearchResult();
}

/// Search ran and returned an immutable ranked hit list.
final class SearchEnabled extends SearchResult {
  /// Creates a successful result.
  SearchEnabled(Iterable<SearchHit> hits)
    : hits = List<SearchHit>.unmodifiable(hits);

  /// Ranked hits in stable `(score DESC, raw key ASC)` order, possibly empty.
  final List<SearchHit> hits;
}

/// The serving endpoint has its search index disabled.
final class SearchDisabled extends SearchResult {
  /// Creates the calm disabled state while retaining the typed cause.
  const SearchDisabled(this.cause);

  /// Server FailedPrecondition retained for diagnostics.
  final LanternFailedPreconditionException cause;
}

/// Incremental search delivery phase.
enum SearchUpdatePhase {
  /// Input is empty or shorter than the configured minimum.
  idle,

  /// The latest query has an active RPC.
  loading,

  /// The latest query completed with ranked results.
  results,

  /// The endpoint's search index is disabled.
  disabled,

  /// The latest live query failed.
  error,
}

/// Latest-query-wins delivery from [IncrementalSearch].
final class SearchUpdate {
  SearchUpdate._({
    required this.query,
    required this.phase,
    Iterable<SearchHit> hits = const [],
    this.error,
  }) : hits = List<SearchHit>.unmodifiable(hits);

  /// Exact input text that owns this state.
  final String query;

  /// UI-friendly lifecycle state.
  final SearchUpdatePhase phase;

  /// Ranked hits for [SearchUpdatePhase.results].
  final List<SearchHit> hits;

  /// Typed error for [SearchUpdatePhase.error], otherwise `null`.
  final Object? error;
}

/// Configuration for a pure-Dart incremental search session.
final class IncrementalSearchOptions {
  /// Creates an incremental search configuration.
  const IncrementalSearchOptions({
    this.debounce = const Duration(milliseconds: 150),
    this.minimumQueryLength = 1,
    this.search = const SearchOptions(),
  });

  /// Quiet period after the newest keystroke.
  final Duration debounce;

  /// Shortest query, counted in Unicode code points, that issues an RPC.
  final int minimumQueryLength;

  /// Options forwarded to every one-shot search.
  final SearchOptions search;
}

/// Screen-owned, latest-query-wins search-as-you-type session.
final class IncrementalSearch {
  IncrementalSearch._(this._client, this._options) {
    if (_options.debounce < Duration.zero) {
      throw _invalidArgumentException('debounce must not be negative');
    }
    if (_options.minimumQueryLength < 0) {
      throw _invalidArgumentException(
        'minimumQueryLength must not be negative',
      );
    }
    _validateSearchOptions(_options.search);
  }

  final LanternClient _client;
  final IncrementalSearchOptions _options;
  final StreamController<SearchUpdate> _updates =
      StreamController<SearchUpdate>.broadcast(sync: true);
  Timer? _timer;
  LanternCancellationToken? _active;
  var _epoch = 0;
  var _disposed = false;

  /// Broadcast state stream. The session itself enforces newest-query wins.
  Stream<SearchUpdate> get updates => _updates.stream;

  /// Records the newest query, canceling debounce and any active older RPC.
  void search(String query) {
    if (_disposed) return;
    final epoch = ++_epoch;
    _timer?.cancel();
    _timer = null;
    _active?.cancel('superseded incremental search');
    _active = null;

    if (query.isEmpty || query.runes.length < _options.minimumQueryLength) {
      _emit(epoch, SearchUpdate._(query: query, phase: SearchUpdatePhase.idle));
      return;
    }
    _timer = Timer(_options.debounce, () => _dispatch(query, epoch));
  }

  Future<void> _dispatch(String query, int epoch) async {
    if (_disposed || epoch != _epoch) return;
    _timer = null;
    final cancellation = LanternCancellationToken();
    _active = cancellation;
    _emit(
      epoch,
      SearchUpdate._(query: query, phase: SearchUpdatePhase.loading),
    );
    try {
      final result = await _client.searchVertices(
        query,
        searchOptions: _options.search,
        options: LanternCallOptions(cancellation: cancellation),
      );
      if (_disposed || epoch != _epoch) return;
      switch (result) {
        case SearchEnabled(:final hits):
          _emit(
            epoch,
            SearchUpdate._(
              query: query,
              phase: SearchUpdatePhase.results,
              hits: hits,
            ),
          );
        case SearchDisabled():
          _emit(
            epoch,
            SearchUpdate._(query: query, phase: SearchUpdatePhase.disabled),
          );
      }
    } on LanternCanceledException {
      // Superseded and disposed searches are intentionally silent.
    } catch (error) {
      _emit(
        epoch,
        SearchUpdate._(
          query: query,
          phase: SearchUpdatePhase.error,
          error: error,
        ),
      );
    } finally {
      if (epoch == _epoch) _active = null;
    }
  }

  void _emit(int epoch, SearchUpdate update) {
    if (_disposed || epoch != _epoch || _updates.isClosed) return;
    _updates.add(update);
  }

  /// Cancels debounce and active RPC state, then closes [updates].
  Future<void> dispose() async {
    if (_disposed) return;
    _disposed = true;
    _epoch++;
    _timer?.cancel();
    _timer = null;
    _active?.cancel('incremental search disposed');
    _active = null;
    await _updates.close();
  }
}

/// Full-text and incremental search operations.
extension LanternSearch on LanternClient {
  /// Runs one complete full-text query.
  ///
  /// A disabled server index returns [SearchDisabled], not a retry loop or an
  /// untyped failure.
  Future<SearchResult> searchVertices(
    String query, {
    SearchOptions searchOptions = const SearchOptions(),
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    _validateSearchOptions(searchOptions);
    final wireOptions = _searchOptionsToProto(searchOptions);
    final request = $graph.SearchVerticesRequest(
      query: query,
      limit: searchOptions.limit,
      prefix: searchOptions.prefix,
      options: wireOptions,
    );
    try {
      final response = await _invoke(
        'SearchVertices',
        _freezeCallOptions(options),
        (raw, headers, signal, onHeader, onTrailer) => raw.searchVertices(
          request,
          headers: headers,
          signal: signal,
          onHeader: onHeader,
          onTrailer: onTrailer,
        ),
      );
      return SearchEnabled(
        response.hits.map(
          (hit) => SearchHit(
            key: hit.key,
            score: _finiteFloatFromProto(hit.score, 'search score'),
          ),
        ),
      );
    } on LanternFailedPreconditionException catch (error) {
      return SearchDisabled(error);
    }
  }

  /// Creates a screen-owned, pure-Dart incremental search session.
  IncrementalSearch incrementalSearch({
    IncrementalSearchOptions options = const IncrementalSearchOptions(),
  }) {
    _ensureOpen();
    return IncrementalSearch._(this, options);
  }
}

void _validateSearchOptions(SearchOptions options) {
  _validateUint32(options.limit, 'limit');
  final min = options.minShouldMatch;
  if (options.matchMode == SearchMatchMode.minShouldMatch) {
    if (min == null || min <= 0) {
      throw _invalidArgumentException(
        'minShouldMatch must be positive for minShouldMatch mode',
      );
    }
  } else if (min != null) {
    throw _invalidArgumentException(
      'minShouldMatch requires minShouldMatch mode',
    );
  }
  if (min != null) _validateUint32(min, 'minShouldMatch');
  final fuzziness = options.fuzziness;
  if (fuzziness != null && (fuzziness < 0 || fuzziness > 2)) {
    throw _invalidArgumentException('fuzziness must be 0, 1, or 2');
  }
}

$graph.SearchOptions? _searchOptionsToProto(SearchOptions options) {
  final present =
      options.matchMode != null ||
      options.minShouldMatch != null ||
      options.phrase != null ||
      options.fuzziness != null ||
      options.prefixTerms != null;
  if (!present) return null;
  return $graph.SearchOptions(
    matchMode: switch (options.matchMode) {
      null => $graph.MatchMode.MATCH_MODE_UNSPECIFIED,
      SearchMatchMode.any => $graph.MatchMode.MATCH_MODE_ANY,
      SearchMatchMode.all => $graph.MatchMode.MATCH_MODE_ALL,
      SearchMatchMode.minShouldMatch => $graph.MatchMode.MATCH_MODE_MIN_SHOULD,
    },
    minShouldMatch: options.minShouldMatch,
    phrase: options.phrase,
    fuzziness: options.fuzziness,
    prefixTerms: options.prefixTerms,
  );
}

void _validateUint32(int value, String name) {
  if (value < 0 || value > 0xffffffff) {
    throw _invalidArgumentException('$name must fit uint32');
  }
}
