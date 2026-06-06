import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type PutVertexResponse = components["schemas"]["v1PutVertexResponse"];
export type PutVertexBody =
  components["schemas"]["LanternServicePutVertexBody"];

/**
 * Calls `LanternService_PutVertex` (PUT `/v1/vertices/{vertex.key}`).
 *
 * Idempotent upsert for a single vertex by key. The key travels in the
 * URL path; the body carries the value oneof and TTL via the wrapping
 * `vertex` object minus its own `key` field.
 */
export async function putVertex(
  client: LanternClient,
  key: string,
  body: PutVertexBody,
  init?: { signal?: AbortSignal },
): Promise<PutVertexResponse> {
  const path = `/v1/vertices/${encodeURIComponent(key)}`;
  const response = await client.request(path, {
    method: "PUT",
    body: JSON.stringify(body),
    signal: init?.signal,
  });
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "PutVertex");
  }
  return (await response.json()) as PutVertexResponse;
}
