import type { ServerStatus } from "~/lib/client/infrastructure/api/get-server-status";
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
    ["Uptime", formatUptime(s.uptimeSeconds)],
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
  ];
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
