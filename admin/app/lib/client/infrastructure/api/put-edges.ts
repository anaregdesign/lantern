import { mutationReply, type MutationReply } from "lantern-sdk/web";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { requireAppliedPutOutcome } from "./put-outcome";
import { flatEdgeToSdkInput } from "./to-flat";
import type { PutEdgesRequest, PutEdgesResponse } from "./types";

export type { PutEdgesRequest, PutEdgesResponse } from "./types";

/**
 * Calls `LanternService.PutEdges` via `lantern-sdk/web`. Idempotent
 * batch upsert; the SDK auto-chunks (#409).
 */
export async function putEdges(
  client: LanternClient,
  request: PutEdgesRequest,
  init?: { signal?: AbortSignal },
): Promise<MutationReply<PutEdgesResponse>> {
  const inputs = (request.edges ?? []).map(flatEdgeToSdkInput);
  try {
    return await mutationReply(async () => {
      const results = await client.putEdges(inputs, init?.signal);
      for (const [index, result] of results.entries()) {
        requireAppliedPutOutcome(
          "PutEdges",
          `edge ${index} (${JSON.stringify(result.tail)} -> ${JSON.stringify(result.head)})`,
          result.outcome,
        );
      }
      return { results };
    });
  } catch (err) {
    throw LanternApiError.fromUnknown("PutEdges", err);
  }
}
