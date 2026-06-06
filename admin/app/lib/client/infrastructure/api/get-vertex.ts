import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type GetVertexResponse = components["schemas"]["v1GetVertexResponse"];
export type Vertex = components["schemas"]["v1Vertex"];

/**
 * Calls `LanternService_GetVertex` (GET `/v1/vertices/{key}`).
 *
 * Returns `null` when the vertex does not exist (HTTP 404 / NOT_FOUND).
 * Any other non-2xx response is thrown as a `LanternApiError`.
 */
export async function getVertex(
  client: LanternClient,
  key: string,
  init?: { signal?: AbortSignal },
): Promise<Vertex | null> {
  const path = `/v1/vertices/${encodeURIComponent(key)}`;
  const response = await client.request(path, {
    method: "GET",
    signal: init?.signal,
  });
  if (response.status === 404) {
    // Drain body to release the connection.
    await response.text().catch(() => undefined);
    return null;
  }
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "GetVertex");
  }
  const body = (await response.json()) as GetVertexResponse;
  return body.vertex ?? null;
}
