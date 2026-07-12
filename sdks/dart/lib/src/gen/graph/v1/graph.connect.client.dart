//
//  Generated code. Do not modify.
//  source: graph/v1/graph.proto
//

import "package:connectrpc/connect.dart" as connect;
import "graph.pb.dart" as graphv1graph;
import "graph.connect.spec.dart" as specs;

extension type LanternServiceClient (connect.Transport _transport) {
  Future<graphv1graph.IlluminateResponse> illuminate(
    graphv1graph.IlluminateRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.illuminate,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1graph.GetVertexResponse> getVertex(
    graphv1graph.GetVertexRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.getVertex,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// GetVertices reads several vertices in one round trip.
  Future<graphv1graph.GetVerticesResponse> getVertices(
    graphv1graph.GetVerticesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.getVertices,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// PutVertex writes a single vertex. Thin facade over PutVertices.
  Future<graphv1graph.PutVertexResponse> putVertex(
    graphv1graph.PutVertexRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.putVertex,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1graph.PutVerticesResponse> putVertices(
    graphv1graph.PutVerticesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.putVertices,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1graph.DeleteVertexResponse> deleteVertex(
    graphv1graph.DeleteVertexRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.deleteVertex,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// DeleteVertices removes several vertices in one round trip.
  Future<graphv1graph.DeleteVerticesResponse> deleteVertices(
    graphv1graph.DeleteVerticesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.deleteVertices,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// ScanVertices streams vertices whose key starts with the given prefix in
  /// ascending order, page by page. Plural-only — prefix scan is inherently
  /// plural.
  Future<graphv1graph.ScanVerticesResponse> scanVertices(
    graphv1graph.ScanVerticesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.scanVertices,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// ScanVertexKeys streams just the KEYS (no values) of vertices whose key
  /// starts with the given prefix, page by page — the wire-efficient backing
  /// RPC for the `keys` CLI verb. A non-empty prefix is REQUIRED. Plural-only.
  Future<graphv1graph.ScanVertexKeysResponse> scanVertexKeys(
    graphv1graph.ScanVertexKeysRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.scanVertexKeys,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// SearchVertices returns vertices ranked by full-text relevance over their
  /// content (key + value), optionally scoped to a key prefix, in stable
  /// (score DESC, raw key ASC) order. Requires the server-side search index
  /// (LANTERN_SEARCH_ENABLED, on by default); returns FAILED_PRECONDITION when
  /// disabled. Plural-only — ranked search is inherently plural.
  Future<graphv1graph.SearchVerticesResponse> searchVertices(
    graphv1graph.SearchVerticesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.searchVertices,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// CountVerticesByPrefix returns the number of live vertices whose key
  /// starts with the given prefix.
  Future<graphv1graph.CountVerticesByPrefixResponse> countVerticesByPrefix(
    graphv1graph.CountVerticesByPrefixRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.countVerticesByPrefix,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// DeleteVerticesByPrefix deletes up to `limit` vertices whose key starts
  /// with the given prefix. Pass `dry_run = true` to preview the count
  /// without mutating state.
  Future<graphv1graph.DeleteVerticesByPrefixResponse> deleteVerticesByPrefix(
    graphv1graph.DeleteVerticesByPrefixRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.deleteVerticesByPrefix,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

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
  Future<graphv1graph.TopVerticesByDegreeResponse> topVerticesByDegree(
    graphv1graph.TopVerticesByDegreeRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.topVerticesByDegree,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1graph.GetEdgeResponse> getEdge(
    graphv1graph.GetEdgeRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.getEdge,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// GetEdges reads several edges in one round trip.
  Future<graphv1graph.GetEdgesResponse> getEdges(
    graphv1graph.GetEdgesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.getEdges,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// AddEdge is non-idempotent (accumulates weight). Thin facade over AddEdges.
  Future<graphv1graph.AddEdgeResponse> addEdge(
    graphv1graph.AddEdgeRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.addEdge,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// AddEdges is non-idempotent (accumulates weight). POST per REST conventions.
  Future<graphv1graph.AddEdgesResponse> addEdges(
    graphv1graph.AddEdgesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.addEdges,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// PutEdge is idempotent (replaces weight). Thin facade over PutEdges.
  Future<graphv1graph.PutEdgeResponse> putEdge(
    graphv1graph.PutEdgeRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.putEdge,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// PutEdges is idempotent (replaces weight). PUT per REST conventions.
  Future<graphv1graph.PutEdgesResponse> putEdges(
    graphv1graph.PutEdgesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.putEdges,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1graph.DeleteEdgeResponse> deleteEdge(
    graphv1graph.DeleteEdgeRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.deleteEdge,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// DeleteEdges removes several edges in one round trip.
  Future<graphv1graph.DeleteEdgesResponse> deleteEdges(
    graphv1graph.DeleteEdgesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.deleteEdges,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// DeleteEdgesByPrefix deletes up to `limit` live edges whose tail key
  /// starts with `tail_prefix` AND whose head key starts with `head_prefix`.
  /// At least one prefix must be non-empty. Pass `dry_run = true` to preview
  /// the count without mutating state. Plural-only — prefix delete is
  /// inherently plural.
  Future<graphv1graph.DeleteEdgesByPrefixResponse> deleteEdgesByPrefix(
    graphv1graph.DeleteEdgesByPrefixRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.deleteEdgesByPrefix,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// ScanEdges streams edges whose tail key starts with `tail_prefix` AND
  /// whose head key starts with `head_prefix`, in ascending (tail, head)
  /// order, page by page. Plural-only — prefix scan is inherently plural.
  Future<graphv1graph.ScanEdgesResponse> scanEdges(
    graphv1graph.ScanEdgesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.scanEdges,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// GetServerStatus returns a flat snapshot of the server's identity,
  /// build info, configuration ceilings, and current live vertex/edge
  /// counts. Read-only and cheap — intended for the admin UI's "Ops"
  /// tab and lightweight smoke-test tooling. Auth is the caller's
  /// responsibility; no PII is returned.
  Future<graphv1graph.GetServerStatusResponse> getServerStatus(
    graphv1graph.GetServerStatusRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.getServerStatus,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// GetReplicationStatus returns a flat snapshot of the local node's
  /// outbound peer-replication state. Read-only — no peer add/remove
  /// surface is exposed (see #315 out-of-scope). Cheap to call from a
  /// dashboard at any cadence the operator finds useful. On
  /// single-instance deployments enabled=false and peers is empty.
  Future<graphv1graph.GetReplicationStatusResponse> getReplicationStatus(
    graphv1graph.GetReplicationStatusRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternService.getReplicationStatus,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// BackupSnapshot streams a whole-graph, point-in-time backup: every
  /// live vertex and folded edge as a BackupRecord, materialised under a
  /// single GraphCache lock (SnapshotGraph). Unlike the replication
  /// Snapshot RPC it has NO replication gate — it works on a single node.
  /// vertex_prefix optionally scopes the backup to an induced subgraph.
  /// The restore side replays records through PutVertices / PutEdges, so
  /// there is no dedicated restore RPC.
  Stream<graphv1graph.BackupSnapshotResponse> backupSnapshot(
    graphv1graph.BackupSnapshotRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).server(
      specs.LanternService.backupSnapshot,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }
}
