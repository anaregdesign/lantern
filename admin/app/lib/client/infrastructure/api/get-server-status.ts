import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

/**
 * ServerStatus is the JSON-flat view of GetServerStatusResponse the Ops
 * view consumes. Durations are surfaced as both the raw seconds value
 * (for tooling) and a human-readable label (for the card).
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
}

/**
 * Calls LanternService.GetServerStatus and normalises the response into
 * the flat ServerStatus shape. Connect errors are rethrown as
 * LanternApiError so existing usecase error toasts keep working.
 */
export async function getServerStatus(
  client: LanternClient,
  init?: { signal?: AbortSignal },
): Promise<ServerStatus> {
  try {
    const resp = await client.getServerStatus({}, { signal: init?.signal });
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
      // realistic cache size). Coerce here so the use case layer
      // stays BigInt-free.
      vertexCount: Number(resp.vertexCount),
      edgeCount: Number(resp.edgeCount),
    };
  } catch (err) {
    throw LanternApiError.fromUnknown("GetServerStatus", err);
  }
}
