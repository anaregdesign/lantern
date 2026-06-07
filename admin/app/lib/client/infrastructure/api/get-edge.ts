import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { sdkEdgeToFlat } from "./to-flat";
import type { Edge } from "./types";

export type { Edge } from "./types";

/**
 * Calls `LanternService.GetEdge` via `lantern-sdk/web`. Returns
 * `null` when the edge does not exist; any other failure rethrows as
 * `LanternApiError` (#409).
 */
export async function getEdge(
  client: LanternClient,
  tail: string,
  head: string,
  init?: { signal?: AbortSignal },
): Promise<Edge | null> {
  try {
    return sdkEdgeToFlat(await client.getEdge(tail, head, init?.signal));
  } catch (err) {
    if (LanternApiError.isNotFound(err)) {
      return null;
    }
    throw LanternApiError.fromUnknown("GetEdge", err);
  }
}
