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
      startedAt:
          response.hasStartedAt()
              ? _timestampFromProto(response.startedAt)
              : null,
      uptime: response.hasUptime() ? _durationFromProto(response.uptime) : null,
      defaultTtl:
          response.hasDefaultTtl()
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
    final peers = response.peers
        .map(
          (peer) => ReplicationPeerStatus(
            address: peer.address,
            state: _replicationPeerStateFromProto(peer.state),
            lastEventAt:
                peer.hasLastEventAt()
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
      localNow:
          response.hasLocalNow()
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
