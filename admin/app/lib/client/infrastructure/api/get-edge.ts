import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type GetEdgeResponse = components["schemas"]["v1GetEdgeResponse"];
export type Edge = components["schemas"]["v1Edge"];

/**
 * Calls `LanternService_GetEdge` (GET `/v1/edges/{tail}/{head}`).
 *
 * Returns `null` when the edge does not exist (HTTP 404 / NOT_FOUND).
 */
export async function getEdge(
  client: LanternClient,
  tail: string,
  head: string,
  init?: { signal?: AbortSignal },
): Promise<Edge | null> {
  const path = `/v1/edges/${encodeURIComponent(tail)}/${encodeURIComponent(head)}`;
  const response = await client.request(path, {
    method: "GET",
    signal: init?.signal,
  });
  if (response.status === 404) {
    await response.text().catch(() => undefined);
    return null;
  }
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "GetEdge");
  }
  const body = (await response.json()) as GetEdgeResponse;
  return body.edge ?? null;
}
