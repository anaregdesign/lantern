import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type DeleteEdgeResponse = components["schemas"]["v1DeleteEdgeResponse"];

/**
 * Calls `LanternService_DeleteEdge` (DELETE `/v1/edges/{tail}/{head}`).
 *
 * HTTP 404 is treated as a successful "already gone" outcome so callers
 * can render a single "deleted" path.
 */
export async function deleteEdge(
  client: LanternClient,
  tail: string,
  head: string,
  init?: { signal?: AbortSignal },
): Promise<DeleteEdgeResponse> {
  const path = `/v1/edges/${encodeURIComponent(tail)}/${encodeURIComponent(head)}`;
  const response = await client.request(path, {
    method: "DELETE",
    signal: init?.signal,
  });
  if (response.status === 404) {
    await response.text().catch(() => undefined);
    return { existed: false };
  }
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "DeleteEdge");
  }
  return (await response.json()) as DeleteEdgeResponse;
}
