//
//  Generated code. Do not modify.
//  source: graph/v1/replication.proto
//

import "package:connectrpc/connect.dart" as connect;
import "replication.pb.dart" as graphv1replication;

/// LanternReplicationService carries the peer-to-peer (and CDC) replication
/// surface. It is intentionally a separate service from LanternService so
/// that operators can route, throttle, secure, or even disable replication
/// independently of the public read/write API.
/// NOTE — deviation from the original RFC: the Subscribe RPC was originally
/// specified to live on LanternService. The realised proto split introduces
/// a cyclic file import between graph.proto and replication.proto when both
/// the request/response messages and the RPC live on opposite sides of the
/// boundary. Hosting Subscribe on a dedicated service keeps the import
/// graph one-way (replication.proto → graph.proto) and is documented as
/// part of #178's implementation in docs/replication.md.
abstract final class LanternReplicationService {
  /// Fully-qualified name of the LanternReplicationService service.
  static const name = 'graph.v1.LanternReplicationService';

  /// Subscribe streams replicated mutations to a peer (or CDC consumer)
  /// starting at `from_seq` (inclusive). The server replays any in-buffer
  /// entries first, then streams live mutations as they are appended.
  /// If `from_seq` is below the server's first available seq the call
  /// fails with FAILED_PRECONDITION ("gapped") and the caller must
  /// snapshot + resubscribe. Slow consumers whose channel backs up may
  /// also have the stream terminated with FAILED_PRECONDITION — the
  /// remedy is the same.
  /// No HTTP gateway annotation: replication is intentionally exposed
  /// only as Connect server-streaming (consumed by replication pumps on
  /// peer nodes, never directly from browsers).
  static const subscribe = connect.Spec(
    '/$name/Subscribe',
    connect.StreamType.server,
    graphv1replication.SubscribeRequest.new,
    graphv1replication.SubscribeResponse.new,
  );

  /// Snapshot streams a point-in-time, causally-consistent dump of every
  /// live vertex and edge to a bootstrapping peer. The first frame is a
  /// SnapshotHeader carrying the (cutoff_seq_per_origin, cutoff_hlc) the
  /// server used to materialise the snapshot; the last frame is a
  /// SnapshotFooter with the actual vertex / edge counts streamed.
  /// Bootstrap stitch contract: after receiving the SnapshotFooter the
  /// peer MUST call `Subscribe(from_seq_per_origin = {origin: seq+1 for
  /// each (origin, seq) in cutoff_seq_per_origin})` to pick up the live
  /// tail. Without that the snapshot and the live stream cannot be glued
  /// together without gap or overlap.
  /// No HTTP gateway annotation (parity with Subscribe).
  static const snapshot = connect.Spec(
    '/$name/Snapshot',
    connect.StreamType.server,
    graphv1replication.SnapshotRequest.new,
    graphv1replication.SnapshotResponse.new,
  );

  /// PeerStatus returns the responder's per-origin (last_applied_seq,
  /// last_applied_hlc) map. Used by anti-entropy (#186) as the
  /// convergence safety net for the Subscribe pump: a periodic
  /// PeerStatus poll lets each node detect that the peer's view of
  /// some origin is ahead of its own and request the missing tail
  /// (via Subscribe(from_seq = local+1)) or — when that returns
  /// FailedPrecondition — fall back to Snapshot.
  /// The reply set always includes the responder's OWN origin (taken
  /// from its mutation log's last appended seq + hlc) plus every
  /// remote origin whose mutation has ever been applied via
  /// ApplyMutation since process start. Origins the responder has
  /// never seen are simply absent from the map.
  /// The RPC is intentionally unary and stateless — callers can
  /// multiplex it across many peers without holding any server-side
  /// resources. Returns Unavailable when the mutation log is unset
  /// (single-instance test path).
  static const peerStatus = connect.Spec(
    '/$name/PeerStatus',
    connect.StreamType.unary,
    graphv1replication.PeerStatusRequest.new,
    graphv1replication.PeerStatusResponse.new,
  );
}
