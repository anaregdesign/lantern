import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { Vertex } from "./types";

export type { Vertex } from "./types";

/**
 * Calls `LanternService.GetVertex` over Connect-Web.
 *
 * Returns `null` when the vertex does not exist (CodeNotFound).
 * Any other failure is rethrown as a `LanternApiError` so existing
 * usecase error toasts surface unchanged.
 */
export async function getVertex(
  client: LanternClient,
  key: string,
  init?: { signal?: AbortSignal },
): Promise<Vertex | null> {
  try {
    const resp = await client.getVertex({ key }, { signal: init?.signal });
    if (!resp.vertex) {
      return null;
    }
    return resp.vertex.toJson() as Vertex;
  } catch (err) {
    if (LanternApiError.isNotFound(err)) {
      return null;
    }
    throw LanternApiError.fromUnknown("GetVertex", err);
  }
}
