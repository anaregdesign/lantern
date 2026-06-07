import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { DeleteVertexResponse } from "./types";

export type { DeleteVertexResponse } from "./types";

/**
 * Calls `LanternService.DeleteVertex` via `lantern-sdk/web`. A
 * NotFound from the server is normalised to `{ existed: false }` so
 * callers can collapse the "deleted" and "wasn't there" paths into a
 * single branch (#409).
 */
export async function deleteVertex(
  client: LanternClient,
  key: string,
  init?: { signal?: AbortSignal },
): Promise<DeleteVertexResponse> {
  try {
    const existed = await client.deleteVertex(key, init?.signal);
    return { existed };
  } catch (err) {
    if (LanternApiError.isNotFound(err)) {
      return { existed: false };
    }
    throw LanternApiError.fromUnknown("DeleteVertex", err);
  }
}
