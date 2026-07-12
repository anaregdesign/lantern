import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/google/protobuf/duration.pb.dart'
    as duration_proto;
import 'package:lantern_client/src/gen/google/protobuf/timestamp.pb.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test(
    'degree ranking preserves direction, exact count, and server order',
    () async {
      late graph.TopVerticesByDegreeRequest captured;
      final maxUint64 = Int64.fromInts(0xffffffff, 0xffffffff);
      final transport = FakeTransportBuilder()
          .unary<
            graph.TopVerticesByDegreeRequest,
            graph.TopVerticesByDegreeResponse
          >(LanternService.topVerticesByDegree, (request, context) {
            captured = request.clone();
            return graph.TopVerticesByDegreeResponse(
              entries: [
                graph.TopVerticesByDegreeResponse_Entry(
                  key: 'p:b',
                  degree: maxUint64,
                  weightedDegree: 4.5,
                ),
                graph.TopVerticesByDegreeResponse_Entry(
                  key: 'p:a',
                  degree: Int64(2),
                  weightedDegree: 4.5,
                ),
              ],
            );
          })
          .build();
      final entries = await _client(transport).topVerticesByDegree(
        prefix: 'p:',
        limit: 2,
        direction: DegreeDirection.incoming,
        weighted: true,
      );

      expect(entries.map((entry) => entry.key), ['p:b', 'p:a']);
      expect(entries.first.degree, (BigInt.one << 64) - BigInt.one);
      expect(entries.first.weightedDegree, 4.5);
      expect(captured.k, 2);
      expect(
        captured.direction,
        graph.TopVerticesByDegreeRequest_Direction.DIRECTION_IN,
      );
      expect(captured.weighted, isTrue);
    },
  );

  test('degree ranking requires a scoped prefix before transport', () async {
    var calls = 0;
    final transport = FakeTransportBuilder()
        .unary<
          graph.TopVerticesByDegreeRequest,
          graph.TopVerticesByDegreeResponse
        >(LanternService.topVerticesByDegree, (request, context) {
          calls++;
          return graph.TopVerticesByDegreeResponse();
        })
        .build();
    await expectLater(
      _client(transport).topVerticesByDegree(prefix: ''),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(calls, 0);
  });

  test(
    'server status converts exact timestamps, durations, and uint64',
    () async {
      final started = DateTime.parse('2026-07-12T01:02:03Z');
      final transport = FakeTransportBuilder()
          .unary<graph.GetServerStatusRequest, graph.GetServerStatusResponse>(
            LanternService.getServerStatus,
            (request, context) => graph.GetServerStatusResponse(
              version: 'v0.1.0',
              goVersion: 'go-test',
              startedAt: Timestamp.fromDateTime(started),
              uptime: duration_proto.Duration(seconds: Int64(90)),
              defaultTtl: duration_proto.Duration(seconds: Int64(300)),
              maxBatchSize: 65536,
              maxKeyBytes: 1024,
              scanDefaultLimit: 100,
              scanMaxLimit: 1000,
              tlsEnabled: true,
              replicationEnabled: false,
              vertexCount: Int64(7),
              edgeCount: Int64(8),
              search: graph.SearchCapabilities(
                enabled: true,
                positionsEnabled: false,
                defaultLimit: 25,
                maxLimit: 250,
                defaultMatchMode: graph.MatchMode.MATCH_MODE_MIN_SHOULD,
                defaultMinShouldMatch: 2,
                maxFuzziness: 2,
                analyzerVersion: 'script-aware-v1',
                projectionVersion: 'vertex-key-value-v1',
                configFingerprint: 'abc123',
                maxDocumentBytes: 100,
                maxDocumentTokens: 90,
                maxDocumentTerms: 80,
                maxLiveTerms: Int64(70),
                maxLivePostings: Int64(60),
                maxPositionEntries: Int64(50),
                compactionRatio: 2.5,
                compactionMinRetired: Int64(40),
                indexStats: graph.SearchIndexStats(
                  health: graph.SearchIndexHealth.SEARCH_INDEX_HEALTH_HEALTHY,
                  documents: Int64(3),
                  liveTerms: Int64(4),
                  retainedTermSlots: Int64(5),
                  estimatedRetainedBytes: Int64(120),
                  rebuildCount: Int64(2),
                ),
              ),
            ),
          )
          .build();
      final status = await _client(transport).getServerStatus();

      expect(status.version, 'v0.1.0');
      expect(status.goVersion, 'go-test');
      expect(status.startedAt, started);
      expect(status.uptime, const Duration(seconds: 90));
      expect(status.defaultTtl, const Duration(minutes: 5));
      expect(status.maxBatchSize, 65536);
      expect(status.tlsEnabled, isTrue);
      expect(status.vertexCount, BigInt.from(7));
      expect(status.edgeCount, BigInt.from(8));
      expect(status.search.enabled, isTrue);
      expect(status.search.positionsEnabled, isFalse);
      expect(status.search.defaultMatchMode, SearchMatchMode.minShouldMatch);
      expect(status.search.defaultMinShouldMatch, 2);
      expect(status.search.maxFuzziness, 2);
      expect(status.search.configFingerprint, 'abc123');
      expect(status.search.maxDocumentBytes, 100);
      expect(status.search.maxLivePostings, BigInt.from(60));
      expect(status.search.compactionRatio, 2.5);
      expect(status.search.indexStats.health, SearchIndexHealth.healthy);
      expect(status.search.indexStats.estimatedRetainedBytes, BigInt.from(120));
      expect(status.search.indexStats.rebuildCount, BigInt.from(2));
    },
  );

  test(
    'replication snapshot sorts peers and computes server-clock lag',
    () async {
      final now = DateTime.parse('2026-07-12T02:00:00Z');
      final transport = FakeTransportBuilder()
          .unary<
            graph.GetReplicationStatusRequest,
            graph.GetReplicationStatusResponse
          >(
            LanternService.getReplicationStatus,
            (request, context) => graph.GetReplicationStatusResponse(
              nodeId: 'abc',
              localNow: Timestamp.fromDateTime(now),
              enabled: true,
              peers: [
                graph.ReplicationPeer(
                  address: 'z:6380',
                  state: graph.ReplicationPeer_State.STATE_BACKOFF,
                  appliedSeq: Int64(4),
                  error: 'offline',
                ),
                graph.ReplicationPeer(
                  address: 'a:6380',
                  state: graph.ReplicationPeer_State.STATE_STREAMING,
                  lastEventAt: Timestamp.fromDateTime(
                    now.subtract(const Duration(seconds: 5)),
                  ),
                  appliedSeq: Int64(9),
                ),
              ],
            ),
          )
          .build();
      final status = await _client(transport).getReplicationStatus();

      expect(status.enabled, isTrue);
      expect(status.peers.map((peer) => peer.address), ['a:6380', 'z:6380']);
      expect(status.peers.first.state, ReplicationPeerState.streaming);
      expect(status.peers.last.state, ReplicationPeerState.backoff);
      expect(status.peers.last.error, 'offline');
      expect(status.lag(status.peers.first), const Duration(seconds: 5));
      expect(status.lag(status.peers.last), isNull);
    },
  );
}

LanternClient _client(connect.Transport transport) => LanternClient.connect(
  Uri.parse('https://example.test'),
  transport: transport,
);
