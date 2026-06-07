import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { sdkVertexToFlat } from "./to-flat";
import type { Vertex } from "./types";

export type { Vertex } from "./types";

/**
 * Calls `LanternService.GetVertex` via `lantern-sdk/web`.
 *
 * Returns `null` when the vertex does not exist (the SDK raises
 * `NotFoundError`). Any other failure is rethrown as
 * `LanternApiError` so existing usecase error toasts surface
 * unchanged (#409).
 */
export async function getVertex(
  client: LanternClient,
  key: string,
  init?: { signal?: AbortSignal },
): Promise<Vertex | null> {
  try {
    return sdkVertexToFlat(await client.getVertex(key, init?.signal));
  } catch (err) {
    if (LanternApiError.isNotFound(err)) {
      return null;
    }
    throw LanternApiError.fromUnknown("GetVertex", err);
  }
}
