import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type PutEdgeResponse = components["schemas"]["v1PutEdgeResponse"];
export type PutEdgeBody = components["schemas"]["LanternServicePutEdgeBody"];

/**
 * Calls `LanternService_PutEdge` (PUT `/v1/edges/{edge.tail}/{edge.head}`).
 *
 * Idempotent: overwrites the (tail, head) edge with the supplied weight
 * and expiration. The CRUD UI surfaces this side-by-side with
 * `AddEdge` so users see the additive vs replacing distinction.
 */
export async function putEdge(
  client: LanternClient,
  tail: string,
  head: string,
  body: PutEdgeBody,
  init?: { signal?: AbortSignal },
): Promise<PutEdgeResponse> {
  const path = `/v1/edges/${encodeURIComponent(tail)}/${encodeURIComponent(head)}`;
  const response = await client.request(path, {
    method: "PUT",
    body: JSON.stringify(body),
    signal: init?.signal,
  });
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "PutEdge");
  }
  return (await response.json()) as PutEdgeResponse;
}
