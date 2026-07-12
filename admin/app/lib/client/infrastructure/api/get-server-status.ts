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
  search: SearchCapabilities;
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
  maxInFlight: number;
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
        maxInFlight: resp.search?.maxInFlight ?? 0,
      },
    };
  } catch (err) {
    throw LanternApiError.fromUnknown("GetServerStatus", err);
  }
}
