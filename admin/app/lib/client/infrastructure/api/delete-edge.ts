import { mutationReply, type MutationReply } from "lantern-sdk/web";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

/**
 * Calls `LanternService.DeleteEdge` via `lantern-sdk/web`. A NotFound
 * (edge already gone) is normalised to `false` so callers can
 * collapse the "deleted" and "wasn't there" paths (#409).
 */
export async function deleteEdge(
  client: LanternClient,
  tail: string,
  head: string,
  init?: { signal?: AbortSignal },
): Promise<MutationReply<{ existed: boolean }>> {
  try {
    return await mutationReply(async () => {
      try {
        const existed = await client.deleteEdge(tail, head, init?.signal);
        return { existed };
      } catch (err) {
        if (LanternApiError.isNotFound(err)) return { existed: false };
        throw err;
      }
    });
  } catch (err) {
    throw LanternApiError.fromUnknown("DeleteEdge", err);
  }
}
