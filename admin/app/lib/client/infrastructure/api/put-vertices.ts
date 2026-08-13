import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { requireAppliedPutOutcome } from "./put-outcome";
import { flatVertexToSdkInput } from "./to-flat";
import type { PutVerticesRequest, PutVerticesResponse } from "./types";

export type { PutVerticesRequest, PutVerticesResponse } from "./types";

/**
 * Calls `LanternService.PutVertices` via `lantern-sdk/web`.
 * Idempotent batch upsert (#409).
 *
 * The SDK auto-chunks the batch at `ConnectOptions.batchChunkSize`
 * (default 1000); admin's bulk-edit screens can hand over an
 * arbitrarily large array without chunking themselves.
 */
export async function putVertices(
  client: LanternClient,
  request: PutVerticesRequest,
  init?: { signal?: AbortSignal },
): Promise<PutVerticesResponse> {
  const inputs = (request.vertices ?? []).map(flatVertexToSdkInput);
  try {
    const results = await client.putVertices(inputs, init?.signal);
    for (const [index, result] of results.entries()) {
      requireAppliedPutOutcome(
        "PutVertices",
        `vertex ${index} (${JSON.stringify(result.key)})`,
        result.outcome,
      );
    }
    return { results };
  } catch (err) {
    throw LanternApiError.fromUnknown("PutVertices", err);
  }
}
