part of 'client.dart';

/// Direction used by cold-start degree ranking.
enum DegreeDirection {
  /// Count edges leaving each candidate.
  out,

  /// Count edges entering each candidate.
  incoming,

  /// Count both incoming and outgoing edges.
  both,
}

/// Whether the local derived search index can safely serve results.
enum SearchIndexHealth {
  /// No recognized state was supplied.
  unspecified,

  /// Search indexing is disabled on this endpoint.
  disabled,

  /// The complete derived index is safe to serve.
  healthy,

  /// Graph state converged but the derived index requires a rebuild.
  incomplete,
}

/// Observable logical and retained search-index capacity snapshot.
final class SearchIndexStats {
  const SearchIndexStats._({
    required this.health,
    required this.documents,
    required this.physicalDocuments,
    required this.expiredDocuments,
    required this.expirationQueueEntries,
    required this.expirationPurged,
    required this.lastExpirationPurgeDuration,
    required this.liveTerms,
    required this.retainedTermSlots,
    required this.retainedOrdinals,
    required this.postings,
    required this.positionEntries,
    required this.estimatedLiveBytes,
    required this.estimatedRetainedBytes,
    required this.rebuildCount,
    required this.lastRebuildDuration,
  });

  /// Current consistency state.
  final SearchIndexHealth health;

  /// Indexed live documents.
  final BigInt documents;

  /// Physically retained documents before bounded expiration purging.
  final BigInt physicalDocuments;

  /// Physically retained documents that are no longer live.
  final BigInt expiredDocuments;

  /// Pending expiration records retained by the bounded min-heap.
  final BigInt expirationQueueEntries;

  /// Documents removed by query-time expiration purging.
  final BigInt expirationPurged;

  /// Duration of the latest query-time expiration purge, when supplied.
  final Duration? lastExpirationPurgeDuration;

  /// Distinct live terms.
  final BigInt liveTerms;

  /// Retained term-ID slots, including reusable retired slots.
  final BigInt retainedTermSlots;

  /// Retained document ordinal high-water.
  final BigInt retainedOrdinals;

  /// Live term-document pairs.
  final BigInt postings;

  /// Live positional entries.
  final BigInt positionEntries;

  /// Stable logical estimate of live bytes.
  final BigInt estimatedLiveBytes;

  /// Stable estimate including retained high-water slots.
  final BigInt estimatedRetainedBytes;

  /// Completed compactions and bounded rebuilds.
  final BigInt rebuildCount;

  /// Duration of the latest rebuild, when supplied.
  final Duration? lastRebuildDuration;
}

/// One immutable degree-ranked vertex.
final class DegreeEntry {
  /// Creates one ranked entry.
  const DegreeEntry({
    required this.key,
    required this.degree,
    required this.weightedDegree,
  });

  /// Ranked vertex key.
  final String key;

  /// Live edge count in the chosen direction.
  final BigInt degree;

  /// Live edge-weight sum in the chosen direction.
  final double weightedDegree;
}

/// Discoverable full-text-search contract for one serving endpoint.
final class SearchCapabilities {
  const SearchCapabilities._({
    required this.enabled,
    required this.positionsEnabled,
    required this.defaultLimit,
    required this.maxLimit,
    required this.defaultMatchMode,
    required this.defaultMinShouldMatch,
    required this.maxFuzziness,
    required this.analyzerVersion,
    required this.projectionVersion,
    required this.configFingerprint,
    required this.maxDocumentBytes,
    required this.maxDocumentTokens,
    required this.maxDocumentTerms,
    required this.maxLiveTerms,
    required this.maxLivePostings,
    required this.maxPositionEntries,
    required this.maxExpirationVisits,
    required this.compactionRatio,
    required this.compactionMinRetired,
    required this.indexStats,
  });

  /// Whether full-text search is available.
  final bool enabled;

  /// Whether phrase adjacency can be verified.
  final bool positionsEnabled;

  /// Ranked-hit count used when a request omits its limit.
  final int defaultLimit;

  /// Maximum ranked-hit count accepted per request.
  final int maxLimit;

  /// Server-wide default query-term match mode.
  final SearchMatchMode defaultMatchMode;

  /// Default term threshold for [SearchMatchMode.minShouldMatch].
  final int defaultMinShouldMatch;

  /// Maximum accepted fuzzy edit distance.
  final int maxFuzziness;

  /// Stable analyzer implementation version.
  final String analyzerVersion;

  /// Stable vertex-to-document projection version.
  final String projectionVersion;

  /// SHA-256 capability fingerprint used to compare HA members.
  final String configFingerprint;

  /// Maximum projected bytes per indexed document.
  final int maxDocumentBytes;

  /// Maximum analyzed tokens per document.
  final int maxDocumentTokens;

  /// Maximum distinct terms per document.
  final int maxDocumentTerms;

  /// Aggregate live-term ceiling.
  final BigInt maxLiveTerms;

  /// Aggregate live-posting ceiling.
  final BigInt maxLivePostings;

  /// Aggregate positional-entry ceiling.
  final BigInt maxPositionEntries;

  /// Maximum expired documents purged by one search attempt.
  final BigInt maxExpirationVisits;

  /// Retained-to-live ratio that triggers compaction.
  final double compactionRatio;

  /// Minimum retired slots before ratio-triggered compaction.
  final BigInt compactionMinRetired;

  /// Current index capacity and health snapshot.
  final SearchIndexStats indexStats;
}

/// Explicit point-in-time server status snapshot.
final class ServerStatus {
  ServerStatus._({
    required this.version,
    required this.goVersion,
    required this.startedAt,
    required this.uptime,
    required this.defaultTtl,
    required this.maxBatchSize,
    required this.maxKeyBytes,
    required this.scanDefaultLimit,
    required this.scanMaxLimit,
    required this.tlsEnabled,
    required this.replicationEnabled,
    required this.vertexCount,
    required this.edgeCount,
    required this.search,
  });

  /// Server build/version stamp.
  final String version;

  /// Go runtime version reported by the server.
  final String goVersion;

  /// Ready-to-serve timestamp, when supplied.
  final DateTime? startedAt;

  /// Server-computed uptime, when supplied.
  final Duration? uptime;

  /// Default storage TTL, when supplied.
  final Duration? defaultTtl;

  /// Maximum accepted batch size.
  final int maxBatchSize;

  /// Maximum key size in bytes.
  final int maxKeyBytes;

  /// Default scan page limit.
  final int scanDefaultLimit;

  /// Maximum scan page limit.
  final int scanMaxLimit;

  /// Whether this endpoint terminates TLS.
  final bool tlsEnabled;

  /// Whether this node participates in replication.
  final bool replicationEnabled;

  /// Best-effort live vertex count.
  final BigInt vertexCount;

  /// Best-effort live edge count.
  final BigInt edgeCount;

  /// Full-text-search capability snapshot and HA configuration fingerprint.
  final SearchCapabilities search;
}

/// Replication peer lifecycle state.
enum ReplicationPeerState {
  /// Wire state was omitted or unknown.
  unspecified,

  /// Peer is dialing or awaiting its first frame.
  connecting,

  /// Subscribe is actively receiving frames.
  streaming,

  /// Peer is waiting before a reconnect.
  backoff,

  /// Peer worker has exited.
  closed,
}

/// One immutable row in a replication snapshot.
final class ReplicationPeerStatus {
  /// Creates one peer row.
  const ReplicationPeerStatus({
    required this.address,
    required this.state,
    required this.lastEventAt,
    required this.appliedSequence,
    required this.error,
  });

  /// Stable peer dial target.
  final String address;

  /// Current peer lifecycle state.
  final ReplicationPeerState state;

  /// Time of the last received frame, if any.
  final DateTime? lastEventAt;

  /// Highest consumed peer-local sequence.
  final BigInt appliedSequence;

  /// Last session error, or `null` when clear.
  final String? error;
}

/// Explicit point-in-time replication status snapshot.
final class ReplicationStatus {
  ReplicationStatus._({
    required this.nodeId,
    required this.localNow,
    required this.enabled,
    required Iterable<ReplicationPeerStatus> peers,
  }) : peers = List<ReplicationPeerStatus>.unmodifiable(peers);

  /// Local HLC node identifier.
  final String nodeId;

  /// Server clock captured with this snapshot.
  final DateTime? localNow;

  /// Whether peer replication is configured.
  final bool enabled;

  /// Peers sorted by address for deterministic UI rendering.
  final List<ReplicationPeerStatus> peers;

  /// Server-clock lag for [peer], or `null` before its first event.
  Duration? lag(ReplicationPeerStatus peer) {
    final now = localNow;
    final event = peer.lastEventAt;
    if (now == null || event == null) return null;
    final value = now.difference(event);
    return value.isNegative ? Duration.zero : value;
  }
}

/// Cold-start ranking and explicit status snapshots.
extension LanternStatus on LanternClient {
  /// Returns highest-degree vertices under one required namespace prefix.
  Future<List<DegreeEntry>> topVerticesByDegree({
    required String prefix,
    int limit = 0,
    DegreeDirection direction = DegreeDirection.out,
    bool weighted = false,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    _requireNonEmpty(prefix, 'prefix');
    _validateUint32(limit, 'limit');
    final request = $graph.TopVerticesByDegreeRequest(
      prefix: prefix,
      k: limit,
      direction: _degreeDirectionToProto(direction),
      weighted: weighted,
    );
    final response = await _invoke(
      'TopVerticesByDegree',
      _freezeCallOptions(options),
      (raw, headers, signal, onHeader, onTrailer) => raw.topVerticesByDegree(
        request,
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    return List<DegreeEntry>.unmodifiable(
      response.entries.map(
        (entry) => DegreeEntry(
          key: entry.key,
          degree: _uint64ToBigInt(entry.degree),
          weightedDegree: _finiteFloatFromProto(
            entry.weightedDegree,
            'weighted degree',
          ),
        ),
      ),
    );
  }

  /// Fetches one server status snapshot. No polling is started.
  Future<ServerStatus> getServerStatus({LanternCallOptions? options}) async {
    _ensureOpen();
    final response = await _invoke(
      'GetServerStatus',
      _freezeCallOptions(options),
      (raw, headers, signal, onHeader, onTrailer) => raw.getServerStatus(
        $graph.GetServerStatusRequest(),
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    return ServerStatus._(
      version: response.version,
      goVersion: response.goVersion,
      startedAt: response.hasStartedAt()
          ? _timestampFromProto(response.startedAt)
          : null,
      uptime: response.hasUptime() ? _durationFromProto(response.uptime) : null,
      defaultTtl: response.hasDefaultTtl()
          ? _durationFromProto(response.defaultTtl)
          : null,
      maxBatchSize: response.maxBatchSize,
      maxKeyBytes: response.maxKeyBytes,
      scanDefaultLimit: response.scanDefaultLimit,
      scanMaxLimit: response.scanMaxLimit,
      tlsEnabled: response.tlsEnabled,
      replicationEnabled: response.replicationEnabled,
      vertexCount: _uint64ToBigInt(response.vertexCount),
      edgeCount: _uint64ToBigInt(response.edgeCount),
      search: SearchCapabilities._(
        enabled: response.search.enabled,
        positionsEnabled: response.search.positionsEnabled,
        defaultLimit: response.search.defaultLimit,
        maxLimit: response.search.maxLimit,
        defaultMatchMode: switch (response.search.defaultMatchMode) {
          $graph.MatchMode.MATCH_MODE_ALL => SearchMatchMode.all,
          $graph.MatchMode.MATCH_MODE_MIN_SHOULD =>
            SearchMatchMode.minShouldMatch,
          _ => SearchMatchMode.any,
        },
        defaultMinShouldMatch: response.search.defaultMinShouldMatch,
        maxFuzziness: response.search.maxFuzziness,
        analyzerVersion: response.search.analyzerVersion,
        projectionVersion: response.search.projectionVersion,
        configFingerprint: response.search.configFingerprint,
        maxDocumentBytes: response.search.maxDocumentBytes,
        maxDocumentTokens: response.search.maxDocumentTokens,
        maxDocumentTerms: response.search.maxDocumentTerms,
        maxLiveTerms: _uint64ToBigInt(response.search.maxLiveTerms),
        maxLivePostings: _uint64ToBigInt(response.search.maxLivePostings),
        maxPositionEntries: _uint64ToBigInt(response.search.maxPositionEntries),
        maxExpirationVisits: _uint64ToBigInt(
          response.search.maxExpirationVisits,
        ),
        compactionRatio: response.search.compactionRatio,
        compactionMinRetired: _uint64ToBigInt(
          response.search.compactionMinRetired,
        ),
        indexStats: SearchIndexStats._(
          health: switch (response.search.indexStats.health) {
            $graph.SearchIndexHealth.SEARCH_INDEX_HEALTH_DISABLED =>
              SearchIndexHealth.disabled,
            $graph.SearchIndexHealth.SEARCH_INDEX_HEALTH_HEALTHY =>
              SearchIndexHealth.healthy,
            $graph.SearchIndexHealth.SEARCH_INDEX_HEALTH_INCOMPLETE =>
              SearchIndexHealth.incomplete,
            _ => SearchIndexHealth.unspecified,
          },
          documents: _uint64ToBigInt(response.search.indexStats.documents),
          physicalDocuments: _uint64ToBigInt(
            response.search.indexStats.physicalDocuments,
          ),
          expiredDocuments: _uint64ToBigInt(
            response.search.indexStats.expiredDocuments,
          ),
          expirationQueueEntries: _uint64ToBigInt(
            response.search.indexStats.expirationQueueEntries,
          ),
          expirationPurged: _uint64ToBigInt(
            response.search.indexStats.expirationPurged,
          ),
          lastExpirationPurgeDuration:
              response.search.indexStats.hasLastExpirationPurgeDuration()
              ? _durationFromProto(
                  response.search.indexStats.lastExpirationPurgeDuration,
                )
              : null,
          liveTerms: _uint64ToBigInt(response.search.indexStats.liveTerms),
          retainedTermSlots: _uint64ToBigInt(
            response.search.indexStats.retainedTermSlots,
          ),
          retainedOrdinals: _uint64ToBigInt(
            response.search.indexStats.retainedOrdinals,
          ),
          postings: _uint64ToBigInt(response.search.indexStats.postings),
          positionEntries: _uint64ToBigInt(
            response.search.indexStats.positionEntries,
          ),
          estimatedLiveBytes: _uint64ToBigInt(
            response.search.indexStats.estimatedLiveBytes,
          ),
          estimatedRetainedBytes: _uint64ToBigInt(
            response.search.indexStats.estimatedRetainedBytes,
          ),
          rebuildCount: _uint64ToBigInt(
            response.search.indexStats.rebuildCount,
          ),
          lastRebuildDuration:
              response.search.indexStats.hasLastRebuildDuration()
              ? _durationFromProto(
                  response.search.indexStats.lastRebuildDuration,
                )
              : null,
        ),
      ),
    );
  }

  /// Fetches one replication status snapshot. No polling is started.
  Future<ReplicationStatus> getReplicationStatus({
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final response = await _invoke(
      'GetReplicationStatus',
      _freezeCallOptions(options),
      (raw, headers, signal, onHeader, onTrailer) => raw.getReplicationStatus(
        $graph.GetReplicationStatusRequest(),
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    final peers =
        response.peers
            .map(
              (peer) => ReplicationPeerStatus(
                address: peer.address,
                state: _replicationPeerStateFromProto(peer.state),
                lastEventAt: peer.hasLastEventAt()
                    ? _timestampFromProto(peer.lastEventAt)
                    : null,
                appliedSequence: _uint64ToBigInt(peer.appliedSeq),
                error: peer.error.isEmpty ? null : peer.error,
              ),
            )
            .toList(growable: false)
          ..sort((left, right) => left.address.compareTo(right.address));
    return ReplicationStatus._(
      nodeId: response.nodeId,
      localNow: response.hasLocalNow()
          ? _timestampFromProto(response.localNow)
          : null,
      enabled: response.enabled,
      peers: peers,
    );
  }
}

$graph.TopVerticesByDegreeRequest_Direction _degreeDirectionToProto(
  DegreeDirection direction,
) => switch (direction) {
  DegreeDirection.out =>
    $graph.TopVerticesByDegreeRequest_Direction.DIRECTION_OUT,
  DegreeDirection.incoming =>
    $graph.TopVerticesByDegreeRequest_Direction.DIRECTION_IN,
  DegreeDirection.both =>
    $graph.TopVerticesByDegreeRequest_Direction.DIRECTION_BOTH,
};

ReplicationPeerState _replicationPeerStateFromProto(
  $graph.ReplicationPeer_State state,
) => switch (state) {
  $graph.ReplicationPeer_State.STATE_CONNECTING =>
    ReplicationPeerState.connecting,
  $graph.ReplicationPeer_State.STATE_STREAMING =>
    ReplicationPeerState.streaming,
  $graph.ReplicationPeer_State.STATE_BACKOFF => ReplicationPeerState.backoff,
  $graph.ReplicationPeer_State.STATE_CLOSED => ReplicationPeerState.closed,
  _ => ReplicationPeerState.unspecified,
};
