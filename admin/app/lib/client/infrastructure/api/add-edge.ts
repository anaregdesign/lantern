import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type AddEdgeResponse = components["schemas"]["v1AddEdgeResponse"];
export type AddEdgeBody = components["schemas"]["LanternServiceAddEdgeBody"];

/**
 * Calls `LanternService_AddEdge`
 * (POST `/v1/edges/{edge.tail}/{edge.head}/add`).
 *
 * Non-idempotent: each call accumulates another time-decaying contribution
 * onto the (tail, head) edge. Use `putEdge` instead for idempotent replace.
 */
export async function addEdge(
  client: LanternClient,
  tail: string,
  head: string,
  body: AddEdgeBody,
  init?: { signal?: AbortSignal },
): Promise<AddEdgeResponse> {
  const path = `/v1/edges/${encodeURIComponent(tail)}/${encodeURIComponent(head)}/add`;
  const response = await client.request(path, {
    method: "POST",
    body: JSON.stringify(body),
    signal: init?.signal,
  });
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "AddEdge");
  }
  return (await response.json()) as AddEdgeResponse;
}
