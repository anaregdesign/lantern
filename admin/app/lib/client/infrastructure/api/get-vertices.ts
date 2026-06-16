import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { sdkVertexToFlat } from "./to-flat";
import type { Vertex } from "./types";

export type { Vertex } from "./types";

/**
 * Calls `LanternService.GetVertices` via `lantern-sdk/web` (#627).
 *
 * Used to hydrate content-search hits: `SearchVertices` returns only
 * `{ key, score }`, so the search usecase resolves the ranked keys to
 * full vertices in a single batch read. The SDK partitions the result
 * into `found` / `missing`; missing keys are surfaced so the caller can
 * still render the rank slot (a hit whose vertex expired between the
 * search and the hydration is a real, expected TTL race).
 *
 * `found` ordering is not guaranteed by the wire, so the caller must
 * re-order against the ranked hit list rather than trusting this array.
 */
export async function getVertices(
  client: LanternClient,
  keys: readonly string[],
  init?: { signal?: AbortSignal },
): Promise<{ found: Vertex[]; missing: string[] }> {
  if (keys.length === 0) {
    return { found: [], missing: [] };
  }
  try {
    const { found, missing } = await client.getVertices(keys, init?.signal);
    return { found: found.map(sdkVertexToFlat), missing };
  } catch (err) {
    throw LanternApiError.fromUnknown("GetVertices", err);
  }
}
