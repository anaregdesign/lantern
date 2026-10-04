import { mutationReply, type MutationReply } from "lantern-sdk/web";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { requireAppliedPutOutcome } from "./put-outcome";
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
): Promise<MutationReply<PutEdgeResponse>> {
  const flat: Edge = { ...(body.edge ?? {}), tail, head };
  try {
    return await mutationReply(async () => {
      const outcome = await client.putEdge(
        flatEdgeToSdkInput(flat),
        init?.signal,
      );
      requireAppliedPutOutcome(
        "PutEdge",
        `edge ${JSON.stringify(tail)} -> ${JSON.stringify(head)}`,
        outcome,
      );
      return { outcome };
    });
  } catch (err) {
    throw LanternApiError.fromUnknown("PutEdge", err);
  }
}
