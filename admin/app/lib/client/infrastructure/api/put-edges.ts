import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type PutEdgesRequest = components["schemas"]["v1PutEdgesRequest"];
export type PutEdgesResponse = components["schemas"]["v1PutEdgesResponse"];

/**
 * Calls `LanternService_PutEdges` (PUT `/v1/edges/put`).
 *
 * Idempotent: each (tail, head) pair is overwritten with the supplied
 * weight and expiration. Use this for seeding fixtures; the additive
 * `AddEdges` RPC is what the future CRUD UI will surface for user-driven
 * weight accumulation.
 */
export async function putEdges(
  client: LanternClient,
  request: PutEdgesRequest,
  init?: { signal?: AbortSignal },
): Promise<PutEdgesResponse> {
  const response = await client.request("/v1/edges/put", {
    method: "PUT",
    body: JSON.stringify(request),
    signal: init?.signal,
  });
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "PutEdges");
  }
  return (await response.json()) as PutEdgesResponse;
}
