import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type Vertex = components["schemas"]["v1Vertex"];
export type ScanVerticesRequest =
  components["schemas"]["v1ScanVerticesRequest"];
export type ScanVerticesResponse =
  components["schemas"]["v1ScanVerticesResponse"];

/**
 * Calls `LanternService_ScanVertices` (POST `/v1/vertices/scan`).
 *
 * Pass `cursor` from a previous response's `nextCursor` to fetch the next
 * page. Pass an empty / undefined `cursor` to start at the beginning. The
 * server enforces a default + hard maximum on `limit`; passing `0` (or
 * omitting it) accepts the server default.
 */
export async function scanVertices(
  client: LanternClient,
  request: ScanVerticesRequest,
  init?: { signal?: AbortSignal },
): Promise<ScanVerticesResponse> {
  const response = await client.request("/v1/vertices/scan", {
    method: "POST",
    body: JSON.stringify(request),
    signal: init?.signal,
  });
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "ScanVertices");
  }
  return (await response.json()) as ScanVerticesResponse;
}
