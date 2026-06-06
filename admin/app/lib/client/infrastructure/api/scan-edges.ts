import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type Edge = components["schemas"]["v1Edge"];
export type ScanEdgesRequest = components["schemas"]["v1ScanEdgesRequest"];
export type ScanEdgesResponse = components["schemas"]["v1ScanEdgesResponse"];

/**
 * Calls `LanternService_ScanEdges` (POST `/v1/edges/scan`).
 *
 * Pass `cursor` from a previous response's `nextCursor` to fetch the next
 * page. Either prefix may be empty; both empty scans every edge.
 */
export async function scanEdges(
  client: LanternClient,
  request: ScanEdgesRequest,
  init?: { signal?: AbortSignal },
): Promise<ScanEdgesResponse> {
  const response = await client.request("/v1/edges/scan", {
    method: "POST",
    body: JSON.stringify(request),
    signal: init?.signal,
  });
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "ScanEdges");
  }
  return (await response.json()) as ScanEdgesResponse;
}
