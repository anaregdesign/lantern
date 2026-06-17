import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

/**
 * Calls `LanternService.DeleteVertices` (batch) via `lantern-sdk/web`.
 * Returns the number of vertices that actually existed and were removed
 * (summed across the SDK's auto-chunked sub-batches). Backs the
 * variadic `delete vertex <key> [<key> ...]` CLI grammar (#679).
 */
export async function deleteVertices(
  client: LanternClient,
  keys: readonly string[],
  init?: { signal?: AbortSignal },
): Promise<number> {
  try {
    return await client.deleteVertices(keys, init?.signal);
  } catch (err) {
    throw LanternApiError.fromUnknown("DeleteVertices", err);
  }
}
