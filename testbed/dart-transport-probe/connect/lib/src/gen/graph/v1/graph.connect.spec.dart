//
//  Generated code. Do not modify.
//  source: graph/v1/graph.proto
//

import "package:connectrpc/connect.dart" as connect;
import "graph.pb.dart" as graphv1graph;

abstract final class LanternService {
  /// Fully-qualified name of the LanternService service.
  static const name = 'graph.v1.LanternService';

  static const illuminate = connect.Spec(
    '/$name/Illuminate',
    connect.StreamType.unary,
    graphv1graph.IlluminateRequest.new,
    graphv1graph.IlluminateResponse.new,
  );

  static const getVertex = connect.Spec(
    '/$name/GetVertex',
    connect.StreamType.unary,
    graphv1graph.GetVertexRequest.new,
    graphv1graph.GetVertexResponse.new,
  );

  /// GetVertices reads several vertices in one round trip.
  static const getVertices = connect.Spec(
    '/$name/GetVertices',
    connect.StreamType.unary,
    graphv1graph.GetVerticesRequest.new,
    graphv1graph.GetVerticesResponse.new,
  );

  /// PutVertex writes a single vertex. Thin facade over PutVertices.
  static const putVertex = connect.Spec(
    '/$name/PutVertex',
    connect.StreamType.unary,
    graphv1graph.PutVertexRequest.new,
    graphv1graph.PutVertexResponse.new,
  );

  static const putVertices = connect.Spec(
    '/$name/PutVertices',
    connect.StreamType.unary,
    graphv1graph.PutVerticesRequest.new,
    graphv1graph.PutVerticesResponse.new,
  );

  static const deleteVertex = connect.Spec(
    '/$name/DeleteVertex',
    connect.StreamType.unary,
    graphv1graph.DeleteVertexRequest.new,
    graphv1graph.DeleteVertexResponse.new,
  );

  /// DeleteVertices removes several vertices in one round trip.
  static const deleteVertices = connect.Spec(
    '/$name/DeleteVertices',
    connect.StreamType.unary,
    graphv1graph.DeleteVerticesRequest.new,
    graphv1graph.DeleteVerticesResponse.new,
  );

  /// ScanVertices streams vertices whose key starts with the given prefix in
  /// ascending order, page by page. Plural-only — prefix scan is inherently
  /// plural.
  static const scanVertices = connect.Spec(
    '/$name/ScanVertices',
    connect.StreamType.unary,
    graphv1graph.ScanVerticesRequest.new,
    graphv1graph.ScanVerticesResponse.new,
  );

  /// ScanVertexKeys streams just the KEYS (no values) of vertices whose key
  /// starts with the given prefix, page by page — the wire-efficient backing
  /// RPC for the `keys` CLI verb. A non-empty prefix is REQUIRED. Plural-only.
  static const scanVertexKeys = connect.Spec(
    '/$name/ScanVertexKeys',
    connect.StreamType.unary,
    graphv1graph.ScanVertexKeysRequest.new,
    graphv1graph.ScanVertexKeysResponse.new,
  );

  /// SearchVertices returns vertices ranked by full-text relevance over their
  /// content (key + value), optionally scoped to a key prefix, in stable
  /// (score DESC, raw key ASC) order. Requires the server-side search index
  /// (LANTERN_SEARCH_ENABLED, on by default); returns FAILED_PRECONDITION when
  /// disabled. Plural-only — ranked search is inherently plural.
  static const searchVertices = connect.Spec(
    '/$name/SearchVertices',
    connect.StreamType.unary,
    graphv1graph.SearchVerticesRequest.new,
    graphv1graph.SearchVerticesResponse.new,
  );

  /// CountVerticesByPrefix returns the number of live vertices whose key
  /// starts with the given prefix.
  static const countVerticesByPrefix = connect.Spec(
    '/$name/CountVerticesByPrefix',
    connect.StreamType.unary,
    graphv1graph.CountVerticesByPrefixRequest.new,
    graphv1graph.CountVerticesByPrefixResponse.new,
  );

  /// DeleteVerticesByPrefix deletes up to `limit` vertices whose key starts
  /// with the given prefix. Pass `dry_run = true` to preview the count
  /// without mutating state.
  static const deleteVerticesByPrefix = connect.Spec(
    '/$name/DeleteVerticesByPrefix',
    connect.StreamType.unary,
    graphv1graph.DeleteVerticesByPrefixRequest.new,
    graphv1graph.DeleteVerticesByPrefixResponse.new,
  );

  /// TopVerticesByDegree ranks the most-connected live vertices under a key
  /// prefix by their (weighted) out/in/both degree. Read-only aggregate; a
  /// non-empty prefix is REQUIRED (empty → INVALID_ARGUMENT). Results are
  /// point-in-time best-effort and honour the live-visibility rule (#750).
  /// Cost model (#920): OUT-degree is scoped to the candidates' own out-edges,
  /// but IN and BOTH scan every edge bucket in the graph — O(E_total),
  /// independent of prefix narrowness, because no reverse (head->tails) index
  /// exists. That scan runs without holding the write-blocking aggregate lock
  /// for its full duration, so an IN/BOTH result on a large, actively-written
  /// graph is a best-effort snapshot rather than a single point-in-time view.
  static const topVerticesByDegree = connect.Spec(
    '/$name/TopVerticesByDegree',
    connect.StreamType.unary,
    graphv1graph.TopVerticesByDegreeRequest.new,
    graphv1graph.TopVerticesByDegreeResponse.new,
  );

  static const getEdge = connect.Spec(
    '/$name/GetEdge',
    connect.StreamType.unary,
    graphv1graph.GetEdgeRequest.new,
    graphv1graph.GetEdgeResponse.new,
  );

  /// GetEdges reads several edges in one round trip.
  static const getEdges = connect.Spec(
    '/$name/GetEdges',
    connect.StreamType.unary,
    graphv1graph.GetEdgesRequest.new,
    graphv1graph.GetEdgesResponse.new,
  );

  /// AddEdge is non-idempotent (accumulates weight). Thin facade over AddEdges.
  static const addEdge = connect.Spec(
    '/$name/AddEdge',
    connect.StreamType.unary,
    graphv1graph.AddEdgeRequest.new,
    graphv1graph.AddEdgeResponse.new,
  );

  /// AddEdges is non-idempotent (accumulates weight). POST per REST conventions.
  static const addEdges = connect.Spec(
    '/$name/AddEdges',
    connect.StreamType.unary,
    graphv1graph.AddEdgesRequest.new,
    graphv1graph.AddEdgesResponse.new,
  );

  /// PutEdge is idempotent (replaces weight). Thin facade over PutEdges.
  static const putEdge = connect.Spec(
    '/$name/PutEdge',
    connect.StreamType.unary,
    graphv1graph.PutEdgeRequest.new,
    graphv1graph.PutEdgeResponse.new,
  );

  /// PutEdges is idempotent (replaces weight). PUT per REST conventions.
  static const putEdges = connect.Spec(
    '/$name/PutEdges',
    connect.StreamType.unary,
    graphv1graph.PutEdgesRequest.new,
    graphv1graph.PutEdgesResponse.new,
  );

  static const deleteEdge = connect.Spec(
    '/$name/DeleteEdge',
    connect.StreamType.unary,
    graphv1graph.DeleteEdgeRequest.new,
    graphv1graph.DeleteEdgeResponse.new,
  );

  /// DeleteEdges removes several edges in one round trip.
  static const deleteEdges = connect.Spec(
    '/$name/DeleteEdges',
    connect.StreamType.unary,
    graphv1graph.DeleteEdgesRequest.new,
    graphv1graph.DeleteEdgesResponse.new,
  );

  /// DeleteEdgesByPrefix deletes up to `limit` live edges whose tail key
  /// starts with `tail_prefix` AND whose head key starts with `head_prefix`.
  /// At least one prefix must be non-empty. Pass `dry_run = true` to preview
  /// the count without mutating state. Plural-only — prefix delete is
  /// inherently plural.
  static const deleteEdgesByPrefix = connect.Spec(
    '/$name/DeleteEdgesByPrefix',
    connect.StreamType.unary,
    graphv1graph.DeleteEdgesByPrefixRequest.new,
    graphv1graph.DeleteEdgesByPrefixResponse.new,
  );

  /// ScanEdges streams edges whose tail key starts with `tail_prefix` AND
  /// whose head key starts with `head_prefix`, in ascending (tail, head)
  /// order, page by page. Plural-only — prefix scan is inherently plural.
  static const scanEdges = connect.Spec(
    '/$name/ScanEdges',
    connect.StreamType.unary,
    graphv1graph.ScanEdgesRequest.new,
    graphv1graph.ScanEdgesResponse.new,
  );

  /// GetServerStatus returns a flat snapshot of the server's identity,
  /// build info, configuration ceilings, and current live vertex/edge
  /// counts. Read-only and cheap — intended for the admin UI's "Ops"
  /// tab and lightweight smoke-test tooling. Auth is the caller's
  /// responsibility; no PII is returned.
  static const getServerStatus = connect.Spec(
    '/$name/GetServerStatus',
    connect.StreamType.unary,
    graphv1graph.GetServerStatusRequest.new,
    graphv1graph.GetServerStatusResponse.new,
  );

  /// GetReplicationStatus returns a flat snapshot of the local node's
  /// outbound peer-replication state. Read-only — no peer add/remove
  /// surface is exposed (see #315 out-of-scope). Cheap to call from a
  /// dashboard at any cadence the operator finds useful. On
  /// single-instance deployments enabled=false and peers is empty.
  static const getReplicationStatus = connect.Spec(
    '/$name/GetReplicationStatus',
    connect.StreamType.unary,
    graphv1graph.GetReplicationStatusRequest.new,
    graphv1graph.GetReplicationStatusResponse.new,
  );

  /// BackupSnapshot streams a whole-graph, point-in-time backup: every
  /// live vertex and folded edge as a BackupRecord, materialised under a
  /// single GraphCache lock (SnapshotGraph). Unlike the replication
  /// Snapshot RPC it has NO replication gate — it works on a single node.
  /// vertex_prefix optionally scopes the backup to an induced subgraph.
  /// The restore side replays records through PutVertices / PutEdges, so
  /// there is no dedicated restore RPC.
  static const backupSnapshot = connect.Spec(
    '/$name/BackupSnapshot',
    connect.StreamType.server,
    graphv1graph.BackupSnapshotRequest.new,
    graphv1graph.BackupSnapshotResponse.new,
  );
}
