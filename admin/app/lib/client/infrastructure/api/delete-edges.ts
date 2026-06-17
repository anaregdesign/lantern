import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

/** A (tail, head) pair identifying one edge in a batch delete. */
export interface EdgeRef {
  tail: string;
  head: string;
}

/**
 * Calls `LanternService.DeleteEdges` (batch) via `lantern-sdk/web`.
 * Returns the number of edges that actually existed and were removed
 * (summed across the SDK's auto-chunked sub-batches). Backs the
 * variadic `delete edge <tail> <head> [<tail> <head> ...]` CLI grammar
 * (#679).
 */
export async function deleteEdges(
  client: LanternClient,
  refs: readonly EdgeRef[],
  init?: { signal?: AbortSignal },
): Promise<number> {
  try {
    return await client.deleteEdges(refs, init?.signal);
  } catch (err) {
    throw LanternApiError.fromUnknown("DeleteEdges", err);
  }
}
