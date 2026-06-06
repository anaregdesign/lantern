import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { Edge } from "./types";

export type { Edge } from "./types";

/**
 * Calls `LanternService.GetEdge` over Connect-Web.
 *
 * Returns `null` when the edge does not exist (CodeNotFound). Any
 * other failure is rethrown as a `LanternApiError`.
 */
export async function getEdge(
  client: LanternClient,
  tail: string,
  head: string,
  init?: { signal?: AbortSignal },
): Promise<Edge | null> {
  try {
    const resp = await client.getEdge({ tail, head }, { signal: init?.signal });
    if (!resp.edge) {
      return null;
    }
    return resp.edge.toJson() as Edge;
  } catch (err) {
    if (LanternApiError.isNotFound(err)) {
      return null;
    }
    throw LanternApiError.fromUnknown("GetEdge", err);
  }
}
