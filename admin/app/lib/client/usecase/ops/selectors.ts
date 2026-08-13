import type {
  SearchCapabilities,
  SearchIndexStatus,
  ServerStatus,
} from "~/lib/client/infrastructure/api/get-server-status";
import type {
  ReplicationPeerState,
  ReplicationStatus,
} from "~/lib/client/infrastructure/api/get-replication-status";

/**
 * Pure view-model selectors for the Ops cards. Kept separate from the
 * components so the formatters stay unit-testable without spinning up
 * a React renderer.
 */

/**
 * formatUptime renders an uptime in seconds as a compact human label:
 *   90 → "1m 30s", 3700 → "1h 1m", 86_700 → "1d 0h", 0 → "0s".
 * Days/hours/minutes are truncated rather than rounded so the label
 * never overstates how long the server has been up.
 */
export function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "0s";
  const sec = Math.floor(seconds);
  const days = Math.floor(sec / 86_400);
  const hours = Math.floor((sec % 86_400) / 3600);
  const mins = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${mins}m`;
  if (mins > 0) return `${mins}m ${s}s`;
  return `${s}s`;
}

/**
 * formatDuration renders a TTL in seconds as a short string. Used for
 * the "default TTL" line in the server card.
 */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "-";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86_400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86_400)}d`;
}

/**
 * formatStaleness renders a millisecond delta as the same short
 * "<n>(s|m|h|d) ago" the table column displays. A negative or zero
 * delta returns "-" so the cell never shows nonsense.
 */
export function formatStaleness(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "-";
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return `${sec}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ago`;
  if (sec < 86_400) return `${Math.floor(sec / 3600)}h ago`;
  return `${Math.floor(sec / 86_400)}d ago`;
}

/**
 * formatCount renders a count with thousands separators so the
 * vertex/edge totals on the server card read cleanly at any scale.
 */
export function formatCount(n: number): string {
  if (!Number.isFinite(n)) return "-";
  return n.toLocaleString();
}

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "-";
  if (n < 1024) return `${Math.floor(n)} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = n / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(value >= 10 ? 1 : 2)} ${units[unit]}`;
}

/**
 * peerStatePillIntent maps a ReplicationPeerState onto the Fluent UI
 * "intent" label the table cell uses for the colour pill.
 */
export function peerStatePillIntent(
  s: ReplicationPeerState,
): "success" | "warning" | "error" | "info" {
  switch (s) {
    case "streaming":
      return "success";
    case "connecting":
      return "info";
    case "backoff":
    case "unspecified":
      return "warning";
    case "closed":
      return "error";
  }
}

/**
 * serverCardSummary collapses the wire-level ServerStatus into a
 * compact label set the card renders as a definition list. Returned
 * tuple form (label, value) so the renderer can map directly without
 * worrying about ordering.
 */
export function serverCardSummary(s: ServerStatus): Array<[string, string]> {
  return [
    ["Version", s.version || "(dev)"],
    ["Go runtime", s.goVersion || "(unknown)"],
    // startedAtMs === 0 means the wire response carried no started_at
    // field (an older/pre-#943 server, or one that never marked itself
    // started), so uptime is unknown — render "—" rather than a
    // misleading "0s". A genuinely just-started server sends started_at
    // WITH uptimeSeconds === 0 and still renders "0s"; discriminate on
    // startedAtMs, never on uptimeSeconds.
    ["Uptime", s.startedAtMs === 0 ? "—" : formatUptime(s.uptimeSeconds)],
    ["Default TTL", formatDuration(s.defaultTtlSeconds)],
    ["Max batch size", formatCount(s.maxBatchSize)],
    ["Max key bytes", formatCount(s.maxKeyBytes)],
    [
      "Scan limits",
      `${formatCount(s.scanDefaultLimit)} default / ${formatCount(s.scanMaxLimit)} max`,
    ],
    ["TLS", s.tlsEnabled ? "enabled" : "disabled"],
    ["Replication", s.replicationEnabled ? "enabled" : "disabled"],
    ["Vertices (approx.)", formatCount(s.vertexCount)],
    ["Edges (approx.)", formatCount(s.edgeCount)],
    [
      "Vertex causal metadata",
      s.causalMetadata
        ? causalBudgetLabel(
            s.causalMetadata.vertices.entries,
            s.causalMetadata.vertices.limit,
          )
        : "unavailable — upgrade required",
    ],
    [
      "Edge causal metadata",
      s.causalMetadata
        ? causalBudgetLabel(
            s.causalMetadata.edges.entries,
            s.causalMetadata.edges.limit,
          )
        : "unavailable — upgrade required",
    ],
  ];
}

function causalBudgetLabel(entries: number, limit: number): string {
  if (limit <= 0) return `${formatCount(entries)} / unlimited`;
  return `${formatCount(entries)} / ${formatCount(limit)}`;
}

export function searchHealthIntent(
  health: SearchIndexStatus["health"],
): "success" | "warning" | "error" | "info" {
  switch (health) {
    case "healthy":
      return "success";
    case "incomplete":
      return "error";
    case "disabled":
      return "info";
    case "unspecified":
      return "warning";
  }
}

export function searchStatusSummary(
  search: SearchCapabilities,
): Array<[string, string]> {
  const index = search.index;
  const retainedRatio =
    index.estimatedLiveBytes > 0
      ? index.estimatedRetainedBytes / index.estimatedLiveBytes
      : index.estimatedRetainedBytes > 0
        ? Number.POSITIVE_INFINITY
        : 0;
  const ratioLabel = Number.isFinite(retainedRatio)
    ? `${retainedRatio.toFixed(2)}×`
    : "∞";
  return [
    ["Capability", search.enabled ? "enabled" : "disabled"],
    ["Positions", search.positionsEnabled ? "enabled" : "disabled"],
    [
      "Default query",
      `${search.defaultMatchMode}, ${formatCount(search.defaultLimit)} hits (${formatCount(search.maxLimit)} max)`,
    ],
    [
      "Query budgets",
      `${formatBytes(search.maxQueryBytes)} / ${formatCount(search.maxQueryTerms)} terms / ${formatCount(search.maxInFlight)} in flight`,
    ],
    [
      "Work budgets",
      `${formatCount(search.maxDictionaryVisits)} dictionary / ${formatCount(search.maxPostingVisits)} postings / ${formatCount(search.maxPositionVisits)} positions / ${formatCount(search.maxExpirationVisits)} expiration`,
    ],
    [
      "Document limits",
      `${formatBytes(search.maxDocumentBytes)} / ${formatCount(search.maxDocumentTokens)} tokens / ${formatCount(search.maxDocumentTerms)} terms`,
    ],
    [
      "Index capacity",
      `${formatCount(search.maxLiveTerms)} terms / ${formatCount(search.maxLivePostings)} postings / ${formatCount(search.maxPositionEntries)} positions`,
    ],
    [
      "Documents",
      `${formatCount(index.documents)} live / ${formatCount(index.physicalDocuments)} physical / ${formatCount(index.expiredDocuments)} expired`,
    ],
    [
      "Structures",
      `${formatCount(index.liveTerms)} terms / ${formatCount(index.postings)} postings / ${formatCount(index.positionEntries)} positions`,
    ],
    [
      "Retained storage",
      `${formatBytes(index.estimatedRetainedBytes)} (${ratioLabel} live; ${formatCount(index.retainedTermSlots)} term slots / ${formatCount(index.retainedOrdinals)} ordinals)`,
    ],
    [
      "Expiration",
      `${formatCount(index.expirationQueueEntries)} queued / ${formatCount(index.expirationPurged)} purged / ${formatSeconds(index.lastExpirationPurgeDurationSeconds)} last`,
    ],
    [
      "Rebuilds",
      `${formatCount(index.rebuildCount)} / ${formatSeconds(index.lastRebuildDurationSeconds)} last`,
    ],
    [
      "Compaction",
      `${search.compactionRatio.toFixed(2)}× at ${formatCount(search.compactionMinRetired)} retired`,
    ],
    ["Analyzer", search.analyzerVersion || "(unknown)"],
    ["Projection", search.projectionVersion || "(unknown)"],
    ["Config fingerprint", search.configFingerprint || "(unknown)"],
  ];
}

function formatSeconds(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "-";
  if (seconds < 0.001) return `${(seconds * 1_000_000).toFixed(0)}µs`;
  if (seconds < 1) return `${(seconds * 1000).toFixed(1)}ms`;
  return `${seconds.toFixed(2)}s`;
}

/**
 * replicationCardSummary returns the header rows for the replication
 * card (local node id + local now). The per-peer rows are rendered by
 * the table component directly off ReplicationStatus.peers; that
 * slice stays in domain form because the table needs the typed
 * fields for sorting / pill colour.
 */
export function replicationCardSummary(
  r: ReplicationStatus,
): Array<[string, string]> {
  return [
    ["Local node ID", r.nodeId || "(unknown)"],
    [
      "Local clock",
      r.localNowMs > 0 ? new Date(r.localNowMs).toISOString() : "-",
    ],
    ["Replication", r.enabled ? "enabled" : "disabled (single instance)"],
    ["Peers tracked", formatCount(r.peers.length)],
  ];
}
