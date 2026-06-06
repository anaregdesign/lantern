import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

/**
 * Calls `LanternService.DeleteEdge` over Connect-Web.
 *
 * Returns the `existed` flag from the server. A NotFound (the edge
 * was already gone) is normalised to `false` so callers can collapse
 * the "deleted" and "wasn't there" paths.
 */
export async function deleteEdge(
  client: LanternClient,
  tail: string,
  head: string,
  init?: { signal?: AbortSignal },
): Promise<{ existed: boolean }> {
  try {
    const resp = await client.deleteEdge(
      { tail, head },
      { signal: init?.signal },
    );
    return { existed: resp.existed };
  } catch (err) {
    if (LanternApiError.isNotFound(err)) {
      return { existed: false };
    }
    throw LanternApiError.fromUnknown("DeleteEdge", err);
  }
}
