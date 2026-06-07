import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
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
): Promise<PutEdgesResponse> {
  const inputs = (request.edges ?? []).map(flatEdgeToSdkInput);
  try {
    await client.putEdges(inputs, init?.signal);
    return {};
  } catch (err) {
    throw LanternApiError.fromUnknown("PutEdges", err);
  }
}
