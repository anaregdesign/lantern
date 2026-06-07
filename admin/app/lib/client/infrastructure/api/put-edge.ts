import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { flatEdgeToSdkInput } from "./to-flat";
import type { Edge, PutEdgeBody, PutEdgeResponse } from "./types";

export type { Edge, PutEdgeBody, PutEdgeResponse } from "./types";

/**
 * Calls `LanternService.PutEdge` via `lantern-sdk/web`. Idempotent:
 * overwrites the (tail, head) edge with the supplied weight and
 * expiration (#409).
 */
export async function putEdge(
  client: LanternClient,
  tail: string,
  head: string,
  body: PutEdgeBody,
  init?: { signal?: AbortSignal },
): Promise<PutEdgeResponse> {
  const flat: Edge = { ...(body.edge ?? {}), tail, head };
  try {
    await client.putEdge(flatEdgeToSdkInput(flat), init?.signal);
    return {};
  } catch (err) {
    throw LanternApiError.fromUnknown("PutEdge", err);
  }
}
