import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

/**
 * ServerStatus is the JSON-friendly view of GetServerStatusResponse the
 * Ops view consumes. Durations are surfaced as both the raw seconds
 * value (for tooling) and a human-readable label (for the card).
 *
 * Mirrors the field set of pb.GetServerStatusResponse but flattens
 * google.protobuf.Timestamp / Duration into plain numbers so the
 * downstream selectors stay JSON-clean and serialisable for
 * snapshot-test fixtures.
 */
export interface ServerStatus {
  version: string;
  goVersion: string;
  /** epoch milliseconds; 0 when started_at was unset on the wire. */
  startedAtMs: number;
  /** seconds since start (server-computed); 0 when unset. */
  uptimeSeconds: number;
  /** seconds (server-side default TTL); 0 when unset. */
  defaultTtlSeconds: number;
  maxBatchSize: number;
  maxKeyBytes: number;
  scanDefaultLimit: number;
  scanMaxLimit: number;
  tlsEnabled: boolean;
  replicationEnabled: boolean;
  vertexCount: number;
  edgeCount: number;
  /**
   * null when this status surface is unsupported by the connected server, so
   * the data is unavailable until that server is upgraded. A present all-zero
   * snapshot is supported and remains non-null.
   */
  causalMetadata: CausalMetadataStatus | null;
  search: SearchCapabilities;
}

export interface CausalMetadataKindStatus {
  limit: number;
  entries: number;
  estimatedBytes: number;
  entriesHighWater: number;
  estimatedBytesHighWater: number;
  rejectedTotal: number;
  overLimit: boolean;
  /** epoch milliseconds; 0 when no Delete tombstone is retained. */
  oldestRetentionDeadlineMs: number;
}

export interface CausalMetadataStatus {
  vertices: CausalMetadataKindStatus;
  edges: CausalMetadataKindStatus;
}

export interface SearchCapabilities {
  enabled: boolean;
  positionsEnabled: boolean;
  defaultLimit: number;
  maxLimit: number;
  defaultMatchMode: "any" | "all" | "min-should";
  defaultMinShouldMatch: number;
  maxFuzziness: number;
  analyzerVersion: string;
  projectionVersion: string;
  configFingerprint: string;
  timeoutMs: number;
  maxQueryBytes: number;
  maxQueryTerms: number;
  maxDictionaryVisits: number;
  maxPostingVisits: number;
  maxPositionVisits: number;
  maxExpirationVisits: number;
  maxInFlight: number;
  cursorTtlSeconds: number;
  maxSessions: number;
  maxSessionHits: number;
  maxSessionBytes: number;
  maxDocumentBytes: number;
  maxDocumentTokens: number;
  maxDocumentTerms: number;
  maxLiveTerms: number;
  maxLivePostings: number;
  maxPositionEntries: number;
  compactionRatio: number;
  compactionMinRetired: number;
  index: SearchIndexStatus;
}

export interface SearchIndexStatus {
  health: "disabled" | "healthy" | "incomplete" | "unspecified";
  documents: number;
  physicalDocuments: number;
  expiredDocuments: number;
  expirationQueueEntries: number;
  expirationPurged: number;
  liveTerms: number;
  retainedTermSlots: number;
  retainedOrdinals: number;
  postings: number;
  positionEntries: number;
  estimatedLiveBytes: number;
  estimatedRetainedBytes: number;
  rebuildCount: number;
  lastRebuildDurationSeconds: number;
  lastExpirationPurgeDurationSeconds: number;
  generation: number;
}

/**
 * Calls LanternService.GetServerStatus via `lantern-sdk/web` and
 * normalises the response into the flat ServerStatus shape (#409).
 * SDK errors are rethrown as LanternApiError so existing usecase
 * error toasts keep working.
 */
export async function getServerStatus(
  client: LanternClient,
  init?: { signal?: AbortSignal },
): Promise<ServerStatus> {
  try {
    const resp = await client.getServerStatus(init?.signal);
    // Keep this additive field optional at the adapter boundary so Admin can
    // inspect an older server during rolling upgrades. Preserve absence: it
    // means observability is unavailable, not that usage is zero/unlimited.
    const causalMetadata = (
      resp as typeof resp & {
        causalMetadata?: {
          vertices?: Parameters<typeof causalMetadataKind>[0];
          edges?: Parameters<typeof causalMetadataKind>[0];
        };
      }
    ).causalMetadata;
    return {
      version: resp.version,
      goVersion: resp.goVersion,
      startedAtMs: resp.startedAt
        ? Number(resp.startedAt.seconds) * 1000 +
          Math.floor(resp.startedAt.nanos / 1_000_000)
        : 0,
      uptimeSeconds: resp.uptime ? Number(resp.uptime.seconds) : 0,
      defaultTtlSeconds: resp.defaultTtl ? Number(resp.defaultTtl.seconds) : 0,
      maxBatchSize: resp.maxBatchSize,
      maxKeyBytes: resp.maxKeyBytes,
      scanDefaultLimit: resp.scanDefaultLimit,
      scanMaxLimit: resp.scanMaxLimit,
      tlsEnabled: resp.tlsEnabled,
      replicationEnabled: resp.replicationEnabled,
      // protobuf-es returns BigInt for uint64 fields; the admin UI
      // works in plain numbers (count <= 2^53 is fine for any
      // realistic cache size).
      vertexCount: Number(resp.vertexCount),
      edgeCount: Number(resp.edgeCount),
      causalMetadata: causalMetadata
        ? {
            vertices: causalMetadataKind(causalMetadata.vertices),
            edges: causalMetadataKind(causalMetadata.edges),
          }
        : null,
      search: {
        enabled: resp.search?.enabled ?? false,
        positionsEnabled: resp.search?.positionsEnabled ?? false,
        defaultLimit: resp.search?.defaultLimit ?? 0,
        maxLimit: resp.search?.maxLimit ?? 0,
        defaultMatchMode:
          resp.search?.defaultMatchMode === 2
            ? "all"
            : resp.search?.defaultMatchMode === 3
              ? "min-should"
              : "any",
        defaultMinShouldMatch: resp.search?.defaultMinShouldMatch ?? 0,
        maxFuzziness: resp.search?.maxFuzziness ?? 0,
        analyzerVersion: resp.search?.analyzerVersion ?? "",
        projectionVersion: resp.search?.projectionVersion ?? "",
        configFingerprint: resp.search?.configFingerprint ?? "",
        timeoutMs: resp.search?.timeoutMs ?? 0,
        maxQueryBytes: resp.search?.maxQueryBytes ?? 0,
        maxQueryTerms: resp.search?.maxQueryTerms ?? 0,
        maxDictionaryVisits: Number(resp.search?.maxDictionaryVisits ?? 0n),
        maxPostingVisits: Number(resp.search?.maxPostingVisits ?? 0n),
        maxPositionVisits: Number(resp.search?.maxPositionVisits ?? 0n),
        maxExpirationVisits: Number(resp.search?.maxExpirationVisits ?? 0n),
        maxInFlight: resp.search?.maxInFlight ?? 0,
        cursorTtlSeconds: resp.search?.cursorTtlSeconds ?? 0,
        maxSessions: resp.search?.maxSessions ?? 0,
        maxSessionHits: resp.search?.maxSessionHits ?? 0,
        maxSessionBytes: Number(resp.search?.maxSessionBytes ?? 0n),
        maxDocumentBytes: resp.search?.maxDocumentBytes ?? 0,
        maxDocumentTokens: resp.search?.maxDocumentTokens ?? 0,
        maxDocumentTerms: resp.search?.maxDocumentTerms ?? 0,
        maxLiveTerms: Number(resp.search?.maxLiveTerms ?? 0n),
        maxLivePostings: Number(resp.search?.maxLivePostings ?? 0n),
        maxPositionEntries: Number(resp.search?.maxPositionEntries ?? 0n),
        compactionRatio: resp.search?.compactionRatio ?? 0,
        compactionMinRetired: Number(resp.search?.compactionMinRetired ?? 0n),
        index: {
          health: searchIndexHealth(resp.search?.indexStats?.health),
          documents: Number(resp.search?.indexStats?.documents ?? 0n),
          physicalDocuments: Number(
            resp.search?.indexStats?.physicalDocuments ?? 0n,
          ),
          expiredDocuments: Number(
            resp.search?.indexStats?.expiredDocuments ?? 0n,
          ),
          expirationQueueEntries: Number(
            resp.search?.indexStats?.expirationQueueEntries ?? 0n,
          ),
          expirationPurged: Number(
            resp.search?.indexStats?.expirationPurged ?? 0n,
          ),
          liveTerms: Number(resp.search?.indexStats?.liveTerms ?? 0n),
          retainedTermSlots: Number(
            resp.search?.indexStats?.retainedTermSlots ?? 0n,
          ),
          retainedOrdinals: Number(
            resp.search?.indexStats?.retainedOrdinals ?? 0n,
          ),
          postings: Number(resp.search?.indexStats?.postings ?? 0n),
          positionEntries: Number(
            resp.search?.indexStats?.positionEntries ?? 0n,
          ),
          estimatedLiveBytes: Number(
            resp.search?.indexStats?.estimatedLiveBytes ?? 0n,
          ),
          estimatedRetainedBytes: Number(
            resp.search?.indexStats?.estimatedRetainedBytes ?? 0n,
          ),
          rebuildCount: Number(resp.search?.indexStats?.rebuildCount ?? 0n),
          lastRebuildDurationSeconds: durationSeconds(
            resp.search?.indexStats?.lastRebuildDuration,
          ),
          lastExpirationPurgeDurationSeconds: durationSeconds(
            resp.search?.indexStats?.lastExpirationPurgeDuration,
          ),
          generation: Number(resp.search?.indexStats?.generation ?? 0n),
        },
      },
    };
  } catch (err) {
    throw LanternApiError.fromUnknown("GetServerStatus", err);
  }
}

function causalMetadataKind(
  value:
    | {
        limit: bigint;
        entries: bigint;
        estimatedBytes: bigint;
        entriesHighWater: bigint;
        estimatedBytesHighWater: bigint;
        rejectedTotal: bigint;
        overLimit: boolean;
        oldestRetentionDeadline?: { seconds: bigint; nanos: number };
      }
    | undefined,
): CausalMetadataKindStatus {
  const deadline = value?.oldestRetentionDeadline;
  return {
    limit: Number(value?.limit ?? 0n),
    entries: Number(value?.entries ?? 0n),
    estimatedBytes: Number(value?.estimatedBytes ?? 0n),
    entriesHighWater: Number(value?.entriesHighWater ?? 0n),
    estimatedBytesHighWater: Number(value?.estimatedBytesHighWater ?? 0n),
    rejectedTotal: Number(value?.rejectedTotal ?? 0n),
    overLimit: value?.overLimit ?? false,
    oldestRetentionDeadlineMs: deadline
      ? Number(deadline.seconds) * 1000 + Math.floor(deadline.nanos / 1_000_000)
      : 0,
  };
}

function durationSeconds(
  duration: { seconds: bigint; nanos: number } | undefined,
): number {
  if (!duration) return 0;
  return Number(duration.seconds) + duration.nanos / 1_000_000_000;
}

function searchIndexHealth(
  health: number | undefined,
): SearchIndexStatus["health"] {
  switch (health) {
    case 1:
      return "disabled";
    case 2:
      return "healthy";
    case 3:
      return "incomplete";
    default:
      return "unspecified";
  }
}
