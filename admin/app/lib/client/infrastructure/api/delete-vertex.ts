import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type DeleteVertexResponse =
  components["schemas"]["v1DeleteVertexResponse"];

/**
 * Calls `LanternService_DeleteVertex` (DELETE `/v1/vertices/{key}`).
 *
 * Returns the response payload as-is, including the `existed` flag.
 * HTTP 404 is treated as a successful "already gone" outcome
 * (`{ existed: false }`) so callers can render a single "deleted" path.
 */
export async function deleteVertex(
  client: LanternClient,
  key: string,
  init?: { signal?: AbortSignal },
): Promise<DeleteVertexResponse> {
  const path = `/v1/vertices/${encodeURIComponent(key)}`;
  const response = await client.request(path, {
    method: "DELETE",
    signal: init?.signal,
  });
  if (response.status === 404) {
    await response.text().catch(() => undefined);
    return { existed: false };
  }
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "DeleteVertex");
  }
  return (await response.json()) as DeleteVertexResponse;
}
