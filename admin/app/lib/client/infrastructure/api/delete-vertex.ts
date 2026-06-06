import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { DeleteVertexResponse } from "./types";

export type { DeleteVertexResponse } from "./types";

/**
 * Calls `LanternService.DeleteVertex` over Connect-Web.
 *
 * Returns the response payload as-is, including the `existed` flag.
 * A NotFound from the server is normalised to `{ existed: false }` so
 * callers can render a single "deleted" path.
 */
export async function deleteVertex(
  client: LanternClient,
  key: string,
  init?: { signal?: AbortSignal },
): Promise<DeleteVertexResponse> {
  try {
    const resp = await client.deleteVertex({ key }, { signal: init?.signal });
    return resp.toJson() as DeleteVertexResponse;
  } catch (err) {
    if (LanternApiError.isNotFound(err)) {
      return { existed: false };
    }
    throw LanternApiError.fromUnknown("DeleteVertex", err);
  }
}
