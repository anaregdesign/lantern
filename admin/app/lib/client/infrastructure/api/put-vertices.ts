import type { components } from "./lantern-api.gen";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";

export type PutVerticesRequest = components["schemas"]["v1PutVerticesRequest"];
export type PutVerticesResponse =
  components["schemas"]["v1PutVerticesResponse"];

/**
 * Calls `LanternService_PutVertices` (PUT `/v1/vertices`).
 *
 * Idempotent upsert: each vertex by key replaces any existing value at that
 * key. Used by the admin's seeding scripts and end-to-end tests; not yet
 * surfaced in interactive screens (the CRUD UI lands in F3).
 */
export async function putVertices(
  client: LanternClient,
  request: PutVerticesRequest,
  init?: { signal?: AbortSignal },
): Promise<PutVerticesResponse> {
  const response = await client.request("/v1/vertices", {
    method: "PUT",
    body: JSON.stringify(request),
    signal: init?.signal,
  });
  if (!response.ok) {
    throw await LanternApiError.fromResponse(response, "PutVertices");
  }
  return (await response.json()) as PutVerticesResponse;
}
